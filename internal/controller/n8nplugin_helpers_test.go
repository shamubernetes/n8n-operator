package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

func TestBuildPluginInstallPlan_SkipsDisabledAndSortsPackages(t *testing.T) {
	plugins := []n8nv1alpha1.N8nPlugin{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "plugin-b"},
			Spec: n8nv1alpha1.N8nPluginSpec{
				PackageName: "n8n-nodes-b",
				Version:     "2.0.0",
				Enabled:     boolPtr(true),
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "plugin-a"},
			Spec: n8nv1alpha1.N8nPluginSpec{
				PackageName: "n8n-nodes-a",
				Version:     "1.0.0",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "plugin-disabled"},
			Spec: n8nv1alpha1.N8nPluginSpec{
				PackageName: "n8n-nodes-disabled",
				Enabled:     boolPtr(false),
			},
		},
	}

	plan, err := buildPluginInstallPlan(plugins)
	if err != nil {
		t.Fatalf("buildPluginInstallPlan returned error: %v", err)
	}
	if !plan.Enabled {
		t.Fatalf("expected plan to be enabled")
	}
	if len(plan.PackageSpecs) != 2 {
		t.Fatalf("expected 2 package specs, got %d", len(plan.PackageSpecs))
	}
	if got, want := plan.PackageSpecs[0], "n8n-nodes-a@1.0.0"; got != want {
		t.Fatalf("unexpected first package spec: got %q want %q", got, want)
	}
	if got, want := plan.PackageSpecs[1], "n8n-nodes-b@2.0.0"; got != want {
		t.Fatalf("unexpected second package spec: got %q want %q", got, want)
	}
	if plan.Hash == "" {
		t.Fatalf("expected non-empty plugin hash")
	}
	if plan.DependenciesJSON == "" {
		t.Fatalf("expected non-empty dependencies json")
	}
}

func TestBuildPluginInstallPlan_ConflictingVersionsFail(t *testing.T) {
	plugins := []n8nv1alpha1.N8nPlugin{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "plugin-a"},
			Spec: n8nv1alpha1.N8nPluginSpec{
				PackageName: "n8n-nodes-a",
				Version:     "1.0.0",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "plugin-b"},
			Spec: n8nv1alpha1.N8nPluginSpec{
				PackageName: "n8n-nodes-a",
				Version:     "2.0.0",
			},
		},
	}

	if _, err := buildPluginInstallPlan(plugins); err == nil {
		t.Fatalf("expected conflicting versions to fail")
	}
}

func TestResolvePluginPackageSpec_DefaultsToLatest(t *testing.T) {
	spec := n8nv1alpha1.N8nPluginSpec{
		PackageName: "n8n-nodes-a",
	}

	if got, want := resolvePluginPackageSpec(spec), "n8n-nodes-a@latest"; got != want {
		t.Fatalf("resolved package mismatch: got %q want %q", got, want)
	}
}
