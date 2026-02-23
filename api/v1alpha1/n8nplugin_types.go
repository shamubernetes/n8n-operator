package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// N8nPluginInstanceReference references an N8nInstance in the same namespace.
type N8nPluginInstanceReference struct {
	// Name of the target N8nInstance.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// N8nPluginSpec defines the desired state of N8nPlugin.
type N8nPluginSpec struct {
	// InstanceRef references the target N8nInstance in the same namespace.
	// +kubebuilder:validation:Required
	InstanceRef N8nPluginInstanceReference `json:"instanceRef"`

	// PackageName is the npm package name for the n8n community node package.
	// Examples: n8n-nodes-foo, @acme/n8n-nodes-bar
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=200
	PackageName string `json:"packageName"`

	// Version is the npm version or range to install.
	// If empty, latest is used.
	// +kubebuilder:default="latest"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +optional
	Version string `json:"version,omitempty"`

	// Enabled controls whether this plugin is included in installation.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// N8nPluginStatus defines the observed state of N8nPlugin.
type N8nPluginStatus struct {
	// ResolvedPackage is the exact npm package spec used by the operator.
	// Example: n8n-nodes-foo@1.2.3
	// +optional
	ResolvedPackage string `json:"resolvedPackage,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=n8nplg
// +kubebuilder:printcolumn:name="Instance",type=string,JSONPath=`.spec.instanceRef.name`
// +kubebuilder:printcolumn:name="Package",type=string,JSONPath=`.spec.packageName`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Enabled",type=boolean,JSONPath=`.spec.enabled`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// N8nPlugin is the Schema for the n8nplugins API.
type N8nPlugin struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of N8nPlugin.
	Spec N8nPluginSpec `json:"spec"`

	// Status defines the observed state of N8nPlugin.
	Status N8nPluginStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// N8nPluginList contains a list of N8nPlugin.
type N8nPluginList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []N8nPlugin `json:"items"`
}

func init() {
	SchemeBuilder.Register(&N8nPlugin{}, &N8nPluginList{})
}
