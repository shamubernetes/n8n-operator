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
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

func TestGetWorkflowJSON_FromConfigMap(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := n8nv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add n8n scheme: %v", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "workflow-cm", Namespace: "services"},
		Data: map[string]string{
			"flow.json": `{"nodes":[],"connections":{}}`,
		},
	}

	reconciler := &N8nWorkflowReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build(),
		Scheme: scheme,
	}

	workflow := &n8nv1alpha1.N8nWorkflow{
		ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "services"},
		Spec: n8nv1alpha1.N8nWorkflowSpec{
			SourceRef: &n8nv1alpha1.WorkflowSourceRef{
				Kind: "ConfigMap",
				Name: "workflow-cm",
				Key:  "flow.json",
			},
		},
	}

	payload, err := reconciler.getWorkflowJSON(context.Background(), workflow)
	if err != nil {
		t.Fatalf("getWorkflowJSON failed: %v", err)
	}

	if got, want := string(payload), `{"nodes":[],"connections":{}}`; got != want {
		t.Fatalf("workflow payload mismatch: got %s want %s", got, want)
	}
}

func TestBuildWorkflowPayload(t *testing.T) {
	workflowData := map[string]interface{}{
		"nodes": []interface{}{
			map[string]interface{}{"name": "Trigger", "type": "n8n-nodes-base.scheduleTrigger"},
		},
		"connections": map[string]interface{}{"Trigger": map[string]interface{}{}},
		"settings":    map[string]interface{}{"executionOrder": "v1"},
	}

	wf := buildWorkflowPayload("Nightly sync", workflowData)

	if wf.Name != "Nightly sync" {
		t.Fatalf("name mismatch: %s", wf.Name)
	}
	if len(wf.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(wf.Nodes))
	}
	if _, ok := wf.Connections["Trigger"]; !ok {
		t.Fatalf("missing Trigger connection")
	}
	if got, ok := wf.Settings["executionOrder"]; !ok || got != "v1" {
		t.Fatalf("unexpected settings: %+v", wf.Settings)
	}
}

func TestWorkflowHandleDeletion_ForceFinalizeAfterCleanupTimeout(t *testing.T) {
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
	workflow := &n8nv1alpha1.N8nWorkflow{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "workflow-cleanup",
			Namespace:         "services",
			Finalizers:        []string{n8nWorkflowFinalizer},
			DeletionTimestamp: &deletionTime,
		},
		Spec: n8nv1alpha1.N8nWorkflowSpec{
			WorkflowName:   "Nightly sync",
			DeletionPolicy: n8nv1alpha1.DeletionPolicyDelete,
			N8nInstance: n8nv1alpha1.N8nInstanceRef{
				APIKeySecretRef: n8nv1alpha1.SecretKeyReference{
					Name: "n8n-api",
					Key:  "api-key",
				},
			},
		},
	}

	reconciler := &N8nWorkflowReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiSecret, workflow).Build(),
		Scheme: scheme,
	}

	if _, err := reconciler.handleDeletion(context.Background(), workflow); err != nil {
		t.Fatalf("handleDeletion returned error: %v", err)
	}

	updated := &n8nv1alpha1.N8nWorkflow{}
	if err := reconciler.Get(context.Background(), namespacedName("services", "workflow-cleanup"), updated); err != nil {
		if apierrors.IsNotFound(err) {
			return
		}
		t.Fatalf("get workflow failed: %v", err)
	}
	if hasFinalizer(updated.Finalizers, n8nWorkflowFinalizer) {
		t.Fatalf("finalizer should be removed when timeout forces cleanup bypass")
	}
}

func TestWorkflowHandleDeletion_RetriesBeforeCleanupTimeout(t *testing.T) {
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
	workflow := &n8nv1alpha1.N8nWorkflow{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "workflow-cleanup-retry",
			Namespace:         "services",
			Finalizers:        []string{n8nWorkflowFinalizer},
			DeletionTimestamp: &deletionTime,
		},
		Spec: n8nv1alpha1.N8nWorkflowSpec{
			WorkflowName:   "Nightly retry",
			DeletionPolicy: n8nv1alpha1.DeletionPolicyDelete,
			N8nInstance: n8nv1alpha1.N8nInstanceRef{
				APIKeySecretRef: n8nv1alpha1.SecretKeyReference{
					Name: "n8n-api",
					Key:  "api-key",
				},
			},
		},
	}

	reconciler := &N8nWorkflowReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiSecret, workflow).Build(),
		Scheme: scheme,
	}

	if _, err := reconciler.handleDeletion(context.Background(), workflow); err == nil {
		t.Fatalf("expected cleanup error before timeout, got nil")
	}

	updated := &n8nv1alpha1.N8nWorkflow{}
	if err := reconciler.Get(context.Background(), namespacedName("services", "workflow-cleanup-retry"), updated); err != nil {
		t.Fatalf("get workflow failed: %v", err)
	}
	if !hasFinalizer(updated.Finalizers, n8nWorkflowFinalizer) {
		t.Fatalf("finalizer should remain while retries continue")
	}
}
