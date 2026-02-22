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
)

// DeletionPolicy controls how the operator handles remote n8n resources on CR deletion.
type DeletionPolicy string

const (
	// DeletionPolicyRetain leaves the remote n8n object untouched when the CR is deleted.
	DeletionPolicyRetain DeletionPolicy = "Retain"
	// DeletionPolicyDelete removes the remote n8n object when the CR is deleted.
	DeletionPolicyDelete DeletionPolicy = "Delete"
)

// N8nCredentialSpec defines the desired state of N8nCredential.
type N8nCredentialSpec struct {
	// N8nInstance is the reference to the n8n instance
	// +kubebuilder:validation:Required
	N8nInstance N8nInstanceRef `json:"n8nInstance"`

	// CredentialName is the name of the credential in n8n
	// +kubebuilder:validation:Required
	CredentialName string `json:"credentialName"`

	// CredentialType is the n8n credential type (e.g., postgres, httpHeaderAuth)
	// +kubebuilder:validation:Required
	CredentialType string `json:"credentialType"`

	// DeletionPolicy controls behavior when the CR is deleted.
	// Retain keeps the credential in n8n, Delete removes it from n8n.
	// +kubebuilder:validation:Enum=Retain;Delete
	// +kubebuilder:default=Retain
	// +optional
	DeletionPolicy DeletionPolicy `json:"deletionPolicy,omitempty"`

	// SecretRef references a Kubernetes Secret containing credential data.
	// The Secret can be managed by External Secrets Operator for any backend
	// (1Password, Vault, AWS Secrets Manager, etc.)
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`

	// Data contains static credential data (use secretRef for sensitive values)
	// +optional
	Data map[string]string `json:"data,omitempty"`

	// FieldMappings maps n8n credential fields to Secret keys.
	// Key: n8n credential field name, Value: Secret key name
	// Example: {"password": "db-password", "host": "db-host"}
	// +optional
	FieldMappings map[string]string `json:"fieldMappings,omitempty"`
}

// N8nInstanceRef references an n8n instance
type N8nInstanceRef struct {
	// URL is the direct URL to the n8n API (e.g., http://n8n.services.svc:5678)
	// +optional
	URL string `json:"url,omitempty"`

	// ServiceRef references a Kubernetes Service
	// +optional
	ServiceRef *ServiceReference `json:"serviceRef,omitempty"`

	// APIKeySecretRef references the secret containing the n8n API key
	// +kubebuilder:validation:Required
	APIKeySecretRef SecretKeyReference `json:"apiKeySecretRef"`
}

// ServiceReference references a Kubernetes Service
type ServiceReference struct {
	// Name of the Service
	Name string `json:"name"`
	// Namespace of the Service (defaults to the resource namespace)
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// Port of the Service (defaults to 5678)
	// +optional
	Port int32 `json:"port,omitempty"`
}

// SecretReference references a Kubernetes Secret
type SecretReference struct {
	// Name of the Secret
	Name string `json:"name"`
	// Namespace of the Secret (defaults to the resource namespace)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// SecretKeyReference references a specific key in a Secret
type SecretKeyReference struct {
	// Name of the Secret
	Name string `json:"name"`
	// Key in the Secret
	Key string `json:"key"`
	// Namespace of the Secret (defaults to the resource namespace)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// N8nCredentialStatus defines the observed state of N8nCredential.
type N8nCredentialStatus struct {
	// CredentialID is the ID of the credential in n8n
	// +optional
	CredentialID string `json:"credentialId,omitempty"`

	// Conditions represent the latest available observations of the credential's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastSyncTime is the last time the credential was synced
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// ObservedGeneration is the most recent generation observed
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Credential",type=string,JSONPath=`.spec.credentialName`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.credentialType`
// +kubebuilder:printcolumn:name="ID",type=string,JSONPath=`.status.credentialId`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// N8nCredential is the Schema for the n8ncredentials API.
type N8nCredential struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   N8nCredentialSpec   `json:"spec,omitempty"`
	Status N8nCredentialStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// N8nCredentialList contains a list of N8nCredential.
type N8nCredentialList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []N8nCredential `json:"items"`
}

func init() {
	SchemeBuilder.Register(&N8nCredential{}, &N8nCredentialList{})
}
