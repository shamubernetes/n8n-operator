package controller

import (
	"context"
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

func TestN8nPluginReconcile_UpdatesSiblingStatusesForConflicts(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := n8nv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register n8n scheme: %v", err)
	}

	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
	}
	pluginA := &n8nv1alpha1.N8nPlugin{
		ObjectMeta: metav1.ObjectMeta{Name: "plugin-a", Namespace: "services"},
		Spec: n8nv1alpha1.N8nPluginSpec{
			InstanceRef: n8nv1alpha1.N8nPluginInstanceReference{Name: "n8n"},
			PackageName: "n8n-nodes-foo",
			Version:     "1.0.0",
		},
	}
	pluginB := &n8nv1alpha1.N8nPlugin{
		ObjectMeta: metav1.ObjectMeta{Name: "plugin-b", Namespace: "services"},
		Spec: n8nv1alpha1.N8nPluginSpec{
			InstanceRef: n8nv1alpha1.N8nPluginInstanceReference{Name: "n8n"},
			PackageName: "n8n-nodes-foo",
			Version:     "2.0.0",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&n8nv1alpha1.N8nPlugin{}).
		WithObjects(instance, pluginA, pluginB).
		Build()

	reconciler := &N8nPluginReconciler{Client: client, Scheme: scheme}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "plugin-a", Namespace: "services"},
	})
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	gotA := &n8nv1alpha1.N8nPlugin{}
	if err := client.Get(context.Background(), types.NamespacedName{Name: "plugin-a", Namespace: "services"}, gotA); err != nil {
		t.Fatalf("failed to get plugin-a: %v", err)
	}
	gotB := &n8nv1alpha1.N8nPlugin{}
	if err := client.Get(context.Background(), types.NamespacedName{Name: "plugin-b", Namespace: "services"}, gotB); err != nil {
		t.Fatalf("failed to get plugin-b: %v", err)
	}

	conditionA := apimeta.FindStatusCondition(gotA.Status.Conditions, pluginReadyConditionType)
	conditionB := apimeta.FindStatusCondition(gotB.Status.Conditions, pluginReadyConditionType)
	if conditionA == nil || conditionB == nil {
		t.Fatalf("expected Ready conditions on both plugins")
	}
	if conditionA.Reason != "InvalidPluginSet" || conditionB.Reason != "InvalidPluginSet" {
		t.Fatalf("expected both plugins to report InvalidPluginSet, got %q and %q", conditionA.Reason, conditionB.Reason)
	}
}

func TestN8nPluginReconcile_RefreshesOnDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := n8nv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register n8n scheme: %v", err)
	}

	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
	}
	plugin := &n8nv1alpha1.N8nPlugin{
		ObjectMeta: metav1.ObjectMeta{Name: "plugin-a", Namespace: "services"},
		Spec: n8nv1alpha1.N8nPluginSpec{
			InstanceRef: n8nv1alpha1.N8nPluginInstanceReference{Name: "n8n"},
			PackageName: "n8n-nodes-foo",
			Version:     "1.0.0",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&n8nv1alpha1.N8nPlugin{}).
		WithObjects(instance, plugin).
		Build()
	reconciler := &N8nPluginReconciler{Client: client, Scheme: scheme}

	if err := client.Delete(context.Background(), plugin); err != nil {
		t.Fatalf("failed to delete plugin: %v", err)
	}
	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "plugin-a", Namespace: "services"},
	})
	if err != nil {
		t.Fatalf("reconcile on deleted plugin failed: %v", err)
	}
}

func TestMapInstanceToPlugins_ReturnsMatchingRequests(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := n8nv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register n8n scheme: %v", err)
	}

	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
	}
	pluginMatch := &n8nv1alpha1.N8nPlugin{
		ObjectMeta: metav1.ObjectMeta{Name: "plugin-a", Namespace: "services"},
		Spec: n8nv1alpha1.N8nPluginSpec{
			InstanceRef: n8nv1alpha1.N8nPluginInstanceReference{Name: "n8n"},
			PackageName: "n8n-nodes-a",
		},
	}
	pluginOther := &n8nv1alpha1.N8nPlugin{
		ObjectMeta: metav1.ObjectMeta{Name: "plugin-b", Namespace: "services"},
		Spec: n8nv1alpha1.N8nPluginSpec{
			InstanceRef: n8nv1alpha1.N8nPluginInstanceReference{Name: "other"},
			PackageName: "n8n-nodes-b",
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(instance, pluginMatch, pluginOther).
		Build()

	reconciler := &N8nPluginReconciler{Client: k8sClient, Scheme: scheme}
	requests := reconciler.mapInstanceToPlugins(context.Background(), instance)
	if len(requests) != 1 {
		t.Fatalf("expected exactly one request, got %d", len(requests))
	}
	if got, want := requests[0].NamespacedName, (types.NamespacedName{Name: "plugin-a", Namespace: "services"}); got != want {
		t.Fatalf("unexpected request target: got %v want %v", got, want)
	}
}

func TestMapInstanceToPlugins_NonInstanceObjectReturnsNil(t *testing.T) {
	reconciler := &N8nPluginReconciler{}
	obj := &n8nv1alpha1.N8nPlugin{}
	requests := reconciler.mapInstanceToPlugins(context.Background(), obj)
	if len(requests) != 0 {
		t.Fatalf("expected no requests, got %d", len(requests))
	}
}
