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
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
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

func TestWorkflowUpdateStatus_NoStatusWriteWhenUnchanged(t *testing.T) {
	lastSync := metav1.NewTime(time.Unix(1700000000, 0))
	transition := metav1.NewTime(time.Unix(1700000100, 0))
	workflow := &n8nv1alpha1.N8nWorkflow{
		ObjectMeta: metav1.ObjectMeta{Name: "wf", Namespace: "services", Generation: 5},
		Status: n8nv1alpha1.N8nWorkflowStatus{
			WorkflowID:         "wf-123",
			Active:             false,
			WorkflowHash:       "hash-xyz",
			ObservedGeneration: 5,
			LastSyncTime:       &lastSync,
			Conditions: []metav1.Condition{{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 5,
				LastTransitionTime: transition,
				Reason:             "Synced",
				Message:            "Workflow synced successfully",
			}},
		},
	}

	reconciler := &N8nWorkflowReconciler{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic, status update should be skipped when unchanged: %v", r)
		}
	}()

	result, err := reconciler.updateStatus(
		context.Background(),
		workflow,
		"wf-123",
		false,
		"hash-xyz",
		metav1.ConditionTrue,
		"Synced",
		"Workflow synced successfully",
	)
	if err != nil {
		t.Fatalf("updateStatus returned error: %v", err)
	}
	if result.RequeueAfter != 5*time.Minute {
		t.Fatalf("unexpected requeue: got %s want %s", result.RequeueAfter, 5*time.Minute)
	}
	if workflow.Status.LastSyncTime == nil || !workflow.Status.LastSyncTime.Equal(&lastSync) {
		t.Fatalf("LastSyncTime changed unexpectedly")
	}
}

func TestWorkflowReconcile_SkipsRemoteUpdateWhenAlreadySynced(t *testing.T) {
	var putCalls int32
	var activateCalls int32
	var deactivateCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/workflows":
			if got, want := r.URL.Query().Get("limit"), "250"; got != want {
				t.Fatalf("unexpected limit: got %q want %q", got, want)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{{
					"id":     "wf-123",
					"name":   "Nightly sync",
					"active": false,
				}},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/workflows/wf-123":
			atomic.AddInt32(&putCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     "wf-123",
				"name":   "Nightly sync",
				"active": false,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/workflows/wf-123/activate":
			atomic.AddInt32(&activateCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     "wf-123",
				"name":   "Nightly sync",
				"active": true,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/workflows/wf-123/deactivate":
			atomic.AddInt32(&deactivateCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     "wf-123",
				"name":   "Nightly sync",
				"active": false,
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

	workflowJSON := []byte(`{"nodes":[{"name":"Trigger","type":"n8n-nodes-base.scheduleTrigger","typeVersion":1}],"connections":{},"settings":{"executionOrder":"v1"}}`)
	var workflowData map[string]interface{}
	if err := json.Unmarshal(workflowJSON, &workflowData); err != nil {
		t.Fatalf("unmarshal workflow json: %v", err)
	}
	workflowData["name"] = "Nightly sync"
	desired := buildWorkflowPayload("Nightly sync", workflowData)
	workflowHash, err := hashWorkflowPayload(desired)
	if err != nil {
		t.Fatalf("hashWorkflowPayload failed: %v", err)
	}

	now := metav1.NewTime(time.Unix(1700000200, 0))
	transition := metav1.NewTime(time.Unix(1700000300, 0))
	workflow := &n8nv1alpha1.N8nWorkflow{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "nightly-sync",
			Namespace:  "services",
			Generation: 1,
			Finalizers: []string{n8nWorkflowFinalizer},
		},
		Spec: n8nv1alpha1.N8nWorkflowSpec{
			N8nInstance: n8nv1alpha1.N8nInstanceRef{
				URL: server.URL,
				APIKeySecretRef: n8nv1alpha1.SecretKeyReference{
					Name: "n8n-api",
					Key:  "api-key",
				},
			},
			WorkflowName: "Nightly sync",
			Active:       false,
			Workflow:     &runtime.RawExtension{Raw: workflowJSON},
		},
		Status: n8nv1alpha1.N8nWorkflowStatus{
			WorkflowID:         "wf-123",
			Active:             false,
			WorkflowHash:       workflowHash,
			ObservedGeneration: 1,
			LastSyncTime:       &now,
			Conditions: []metav1.Condition{{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 1,
				LastTransitionTime: transition,
				Reason:             "Synced",
				Message:            "Workflow synced successfully",
			}},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&n8nv1alpha1.N8nWorkflow{}).
		WithObjects(apiSecret, workflow).
		Build()
	reconciler := &N8nWorkflowReconciler{Client: client, Scheme: scheme}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: namespacedName("services", "nightly-sync"),
	})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if result.RequeueAfter != 5*time.Minute {
		t.Fatalf("unexpected requeue: got %s want %s", result.RequeueAfter, 5*time.Minute)
	}
	if got := atomic.LoadInt32(&putCalls); got != 0 {
		t.Fatalf("expected no remote workflow update, got %d PUT calls", got)
	}
	if got := atomic.LoadInt32(&activateCalls); got != 0 {
		t.Fatalf("expected no activation call, got %d", got)
	}
	if got := atomic.LoadInt32(&deactivateCalls); got != 0 {
		t.Fatalf("expected no deactivation call, got %d", got)
	}
}
