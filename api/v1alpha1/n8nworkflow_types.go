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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// N8nWorkflowSpec defines the desired state of N8nWorkflow.
type N8nWorkflowSpec struct {
	// N8nInstance is the reference to the n8n instance
	// +kubebuilder:validation:Required
	N8nInstance N8nInstanceRef `json:"n8nInstance"`

	// WorkflowName is the name of the workflow in n8n
	// +kubebuilder:validation:Required
	WorkflowName string `json:"workflowName"`

	// Active determines if the workflow should be active in n8n
	// +kubebuilder:default=false
	Active bool `json:"active,omitempty"`

	// SourceRef references a ConfigMap containing the workflow JSON
	// +optional
	SourceRef *WorkflowSourceRef `json:"sourceRef,omitempty"`

	// Workflow contains the inline workflow JSON
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Workflow *runtime.RawExtension `json:"workflow,omitempty"`

	// CredentialMappings maps credential references in the workflow to N8nCredential resources
	// Key is the credential name in the workflow, value is the N8nCredential resource name
	// +optional
	CredentialMappings map[string]string `json:"credentialMappings,omitempty"`
}

// WorkflowSourceRef references a ConfigMap or Secret containing workflow JSON
type WorkflowSourceRef struct {
	// Kind is the type of source (ConfigMap or Secret)
	// +kubebuilder:validation:Enum=ConfigMap;Secret
	// +kubebuilder:default=ConfigMap
	Kind string `json:"kind,omitempty"`

	// Name of the ConfigMap or Secret
	Name string `json:"name"`

	// Key in the ConfigMap or Secret containing the workflow JSON
	Key string `json:"key"`

	// Namespace of the source (defaults to the resource namespace)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// N8nWorkflowStatus defines the observed state of N8nWorkflow.
type N8nWorkflowStatus struct {
	// WorkflowID is the ID of the workflow in n8n
	// +optional
	WorkflowID string `json:"workflowId,omitempty"`

	// Active indicates if the workflow is currently active in n8n
	// +optional
	Active bool `json:"active,omitempty"`

	// Conditions represent the latest available observations of the workflow's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastSyncTime is the last time the workflow was synced
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// ObservedGeneration is the most recent generation observed
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// WorkflowHash is the hash of the last applied workflow content
	// +optional
	WorkflowHash string `json:"workflowHash,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Workflow",type=string,JSONPath=`.spec.workflowName`
// +kubebuilder:printcolumn:name="Active",type=boolean,JSONPath=`.status.active`
// +kubebuilder:printcolumn:name="ID",type=string,JSONPath=`.status.workflowId`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// N8nWorkflow is the Schema for the n8nworkflows API.
type N8nWorkflow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   N8nWorkflowSpec   `json:"spec,omitempty"`
	Status N8nWorkflowStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// N8nWorkflowList contains a list of N8nWorkflow.
type N8nWorkflowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []N8nWorkflow `json:"items"`
}

func init() {
	SchemeBuilder.Register(&N8nWorkflow{}, &N8nWorkflowList{})
}
