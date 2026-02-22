/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

func TestBuildCredentialData_MergesMappedSecretAndStaticData(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := n8nv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add n8n scheme: %v", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cred-secret", Namespace: "services"},
		Data: map[string][]byte{
			"DB_USER":     []byte("alice"),
			"DB_PASSWORD": []byte("s3cr3t"),
		},
	}

	reconciler := &N8nCredentialReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build(),
		Scheme: scheme,
	}

	credential := &n8nv1alpha1.N8nCredential{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres-account", Namespace: "services"},
		Spec: n8nv1alpha1.N8nCredentialSpec{
			CredentialName: "Postgres account",
			CredentialType: "postgres",
			SecretRef:      &n8nv1alpha1.SecretReference{Name: "cred-secret"},
			FieldMappings: map[string]string{
				"user":     "DB_USER",
				"password": "DB_PASSWORD",
			},
			Data: map[string]string{
				"host": "db.internal",
				"port": "5432",
			},
		},
	}

	data, err := reconciler.buildCredentialData(context.Background(), credential)
	if err != nil {
		t.Fatalf("buildCredentialData failed: %v", err)
	}

	if got, want := data["host"], "db.internal"; got != want {
		t.Fatalf("host mismatch: got %v want %v", got, want)
	}
	if got, want := data["port"], "5432"; got != want {
		t.Fatalf("port mismatch: got %v want %v", got, want)
	}
	if got, want := data["user"], "alice"; got != want {
		t.Fatalf("user mismatch: got %v want %v", got, want)
	}
	if got, want := data["password"], "s3cr3t"; got != want {
		t.Fatalf("password mismatch: got %v want %v", got, want)
	}
}

func TestNamespacedKeyWithDefault(t *testing.T) {
	if got, want := namespacedKeyWithDefault("", "services", "my-secret"), "services/my-secret"; got != want {
		t.Fatalf("key mismatch: got %s want %s", got, want)
	}
	if got, want := namespacedKeyWithDefault("shared", "services", "my-secret"), "shared/my-secret"; got != want {
		t.Fatalf("key mismatch: got %s want %s", got, want)
	}
}

func TestCredentialHandleDeletion_ForceFinalizeAfterCleanupTimeout(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := n8nv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add n8n scheme: %v", err)
	}

	apiSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "n8n-api", Namespace: "services"},
		Data:       map[string][]byte{"api-key": []byte("token")},
	}
	deletionTime := metav1.NewTime(time.Now().Add(-(cleanupFinalizerMaxRetry + time.Minute)))
	credential := &n8nv1alpha1.N8nCredential{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "postgres-credential",
			Namespace:         "services",
			Finalizers:        []string{n8nCredentialFinalizer},
			DeletionTimestamp: &deletionTime,
		},
		Spec: n8nv1alpha1.N8nCredentialSpec{
			CredentialName: "Postgres account",
			CredentialType: "postgres",
			DeletionPolicy: n8nv1alpha1.DeletionPolicyDelete,
			N8nInstance: n8nv1alpha1.N8nInstanceRef{
				APIKeySecretRef: n8nv1alpha1.SecretKeyReference{
					Name: "n8n-api",
					Key:  "api-key",
				},
			},
		},
	}

	reconciler := &N8nCredentialReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiSecret, credential).Build(),
		Scheme: scheme,
	}

	if _, err := reconciler.handleDeletion(context.Background(), credential); err != nil {
		t.Fatalf("handleDeletion returned error: %v", err)
	}

	updated := &n8nv1alpha1.N8nCredential{}
	if err := reconciler.Get(context.Background(), namespacedName("services", "postgres-credential"), updated); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		t.Fatalf("get credential failed: %v", err)
	}
	if hasFinalizer(updated.Finalizers, n8nCredentialFinalizer) {
		t.Fatalf("finalizer should be removed when timeout forces cleanup bypass")
	}
}

func TestCredentialHandleDeletion_RetriesBeforeCleanupTimeout(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := n8nv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add n8n scheme: %v", err)
	}

	apiSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "n8n-api", Namespace: "services"},
		Data:       map[string][]byte{"api-key": []byte("token")},
	}
	deletionTime := metav1.NewTime(time.Now())
	credential := &n8nv1alpha1.N8nCredential{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "postgres-credential-retry",
			Namespace:         "services",
			Finalizers:        []string{n8nCredentialFinalizer},
			DeletionTimestamp: &deletionTime,
		},
		Spec: n8nv1alpha1.N8nCredentialSpec{
			CredentialName: "Postgres retry",
			CredentialType: "postgres",
			DeletionPolicy: n8nv1alpha1.DeletionPolicyDelete,
			N8nInstance: n8nv1alpha1.N8nInstanceRef{
				APIKeySecretRef: n8nv1alpha1.SecretKeyReference{
					Name: "n8n-api",
					Key:  "api-key",
				},
			},
		},
	}

	reconciler := &N8nCredentialReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiSecret, credential).Build(),
		Scheme: scheme,
	}

	if _, err := reconciler.handleDeletion(context.Background(), credential); err == nil {
		t.Fatalf("expected cleanup error before timeout, got nil")
	}

	updated := &n8nv1alpha1.N8nCredential{}
	if err := reconciler.Get(context.Background(), namespacedName("services", "postgres-credential-retry"), updated); err != nil {
		t.Fatalf("get credential failed: %v", err)
	}
	if !hasFinalizer(updated.Finalizers, n8nCredentialFinalizer) {
		t.Fatalf("finalizer should remain while retries continue")
	}
}

func namespacedName(namespace, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: namespace, Name: name}
}

func hasFinalizer(finalizers []string, target string) bool {
	for _, finalizer := range finalizers {
		if finalizer == target {
			return true
		}
	}
	return false
}
