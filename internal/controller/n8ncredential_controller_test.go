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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
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
	// port should be parsed as int64 since "5432" is a valid integer
	if got, want := data["port"], int64(5432); got != want {
		t.Fatalf("port mismatch: got %v (%T) want %v (%T)", got, got, want, want)
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

func TestCredentialUpdateStatus_NoStatusWriteWhenUnchanged(t *testing.T) {
	lastSync := metav1.NewTime(time.Unix(1700000000, 0))
	transition := metav1.NewTime(time.Unix(1700000100, 0))
	credential := &n8nv1alpha1.N8nCredential{
		ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "services", Generation: 3},
		Status: n8nv1alpha1.N8nCredentialStatus{
			CredentialID:       "cred-123",
			CredentialHash:     "hash-abc",
			ObservedGeneration: 3,
			LastSyncTime:       &lastSync,
			Conditions: []metav1.Condition{{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 3,
				LastTransitionTime: transition,
				Reason:             "Synced",
				Message:            "Credential synced successfully",
			}},
		},
	}

	reconciler := &N8nCredentialReconciler{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic, status update should be skipped when unchanged: %v", r)
		}
	}()

	result, err := reconciler.updateStatus(
		context.Background(),
		credential,
		"cred-123",
		"hash-abc",
		metav1.ConditionTrue,
		"Synced",
		"Credential synced successfully",
	)
	if err != nil {
		t.Fatalf("updateStatus returned error: %v", err)
	}
	if result.RequeueAfter != 5*time.Minute {
		t.Fatalf("unexpected requeue: got %s want %s", result.RequeueAfter, 5*time.Minute)
	}
	if credential.Status.LastSyncTime == nil || !credential.Status.LastSyncTime.Equal(&lastSync) {
		t.Fatalf("LastSyncTime changed unexpectedly")
	}
}

func TestCredentialReconcile_SkipsRemoteUpdateWhenAlreadySynced(t *testing.T) {
	var patchCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/credentials":
			if got, want := r.URL.Query().Get("limit"), "250"; got != want {
				t.Fatalf("unexpected limit: got %q want %q", got, want)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{{
					"id":   "cred-123",
					"name": "Postgres account",
					"type": "postgres",
				}},
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/credentials/cred-123":
			atomic.AddInt32(&patchCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   "cred-123",
				"name": "Postgres account",
				"type": "postgres",
			})
		default:
			t.Fatalf("unexpected n8n request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

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
	credSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "postgres-secret", Namespace: "services"},
		Data:       map[string][]byte{"DB_USER": []byte("alice")},
	}

	credHash, err := hashCredentialPayload("Postgres account", "postgres", map[string]interface{}{
		"host": "db.internal",
		"user": "alice",
	})
	if err != nil {
		t.Fatalf("hashCredentialPayload failed: %v", err)
	}

	now := metav1.NewTime(time.Unix(1700000200, 0))
	transition := metav1.NewTime(time.Unix(1700000300, 0))
	credential := &n8nv1alpha1.N8nCredential{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "postgres-account",
			Namespace:  "services",
			Generation: 1,
			Finalizers: []string{n8nCredentialFinalizer},
		},
		Spec: n8nv1alpha1.N8nCredentialSpec{
			N8nInstance: n8nv1alpha1.N8nInstanceRef{
				URL: server.URL,
				APIKeySecretRef: n8nv1alpha1.SecretKeyReference{
					Name: "n8n-api",
					Key:  "api-key",
				},
			},
			CredentialName: "Postgres account",
			CredentialType: "postgres",
			SecretRef:      &n8nv1alpha1.SecretReference{Name: "postgres-secret"},
			FieldMappings:  map[string]string{"user": "DB_USER"},
			Data:           map[string]string{"host": "db.internal"},
		},
		Status: n8nv1alpha1.N8nCredentialStatus{
			CredentialID:       "cred-123",
			CredentialHash:     credHash,
			ObservedGeneration: 1,
			LastSyncTime:       &now,
			Conditions: []metav1.Condition{{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 1,
				LastTransitionTime: transition,
				Reason:             "Synced",
				Message:            "Credential synced successfully",
			}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&n8nv1alpha1.N8nCredential{}).
		WithObjects(apiSecret, credSecret, credential).
		Build()
	reconciler := &N8nCredentialReconciler{Client: client, Scheme: scheme}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "postgres-account", Namespace: "services"},
	})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.RequeueAfter != 5*time.Minute {
		t.Fatalf("unexpected requeue: got %s want %s", result.RequeueAfter, 5*time.Minute)
	}
	if got := atomic.LoadInt32(&patchCalls); got != 0 {
		t.Fatalf("expected no remote credential update, got %d PATCH calls", got)
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

func TestParseCredentialValue(t *testing.T) {
	tests := []struct {
		input string
		want  interface{}
	}{
		{"5432", int64(5432)},
		{"0", int64(0)},
		{"-1", int64(-1)},
		{"3.14", float64(3.14)},
		{"true", true},
		{"false", false},
		{"hello", "hello"},
		{"", ""},
		{"db.internal", "db.internal"},
		{"password123", "password123"},
	}

	for _, tt := range tests {
		got := parseCredentialValue(tt.input)
		if got != tt.want {
			t.Errorf("parseCredentialValue(%q) = %v (%T), want %v (%T)", tt.input, got, got, tt.want, tt.want)
		}
	}
}
