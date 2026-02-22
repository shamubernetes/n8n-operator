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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
	"github.com/shamubernetes/n8n-operator/pkg/n8n"
)

// N8nCredentialReconciler reconciles a N8nCredential object
type N8nCredentialReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ncredentials,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ncredentials/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ncredentials/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile handles the reconciliation of N8nCredential resources
func (r *N8nCredentialReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the N8nCredential instance
	credential := &n8nv1alpha1.N8nCredential{}
	if err := r.Get(ctx, req.NamespacedName, credential); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("N8nCredential resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get N8nCredential")
		return ctrl.Result{}, err
	}

	// Get the n8n API client
	n8nClient, err := r.getN8nClient(ctx, credential)
	if err != nil {
		logger.Error(err, "Failed to create n8n client")
		return r.updateStatus(ctx, credential, "", metav1.ConditionFalse, "ClientError", err.Error())
	}

	// Build the credential data from Secret + static data
	credData, err := r.buildCredentialData(ctx, credential)
	if err != nil {
		logger.Error(err, "Failed to build credential data")
		return r.updateStatus(ctx, credential, "", metav1.ConditionFalse, "DataError", err.Error())
	}

	// Check if credential exists in n8n
	existingCred, err := n8nClient.GetCredentialByName(ctx, credential.Spec.CredentialName)
	if err != nil {
		logger.Error(err, "Failed to check existing credential")
		return r.updateStatus(ctx, credential, "", metav1.ConditionFalse, "APIError", err.Error())
	}

	var credID string
	if existingCred == nil {
		// Create new credential
		logger.Info("Creating new credential", "name", credential.Spec.CredentialName)
		newCred := &n8n.Credential{
			Name: credential.Spec.CredentialName,
			Type: credential.Spec.CredentialType,
			Data: credData,
		}
		created, err := n8nClient.CreateCredential(ctx, newCred)
		if err != nil {
			logger.Error(err, "Failed to create credential")
			return r.updateStatus(ctx, credential, "", metav1.ConditionFalse, "CreateError", err.Error())
		}
		credID = created.ID
		logger.Info("Created credential", "id", credID)
	} else {
		// Update existing credential
		credID = existingCred.ID
		logger.Info("Updating existing credential", "id", credID)
		updateCred := &n8n.Credential{
			Name: credential.Spec.CredentialName,
			Type: credential.Spec.CredentialType,
			Data: credData,
		}
		_, err := n8nClient.UpdateCredential(ctx, credID, updateCred)
		if err != nil {
			logger.Error(err, "Failed to update credential")
			return r.updateStatus(ctx, credential, credID, metav1.ConditionFalse, "UpdateError", err.Error())
		}
		logger.Info("Updated credential", "id", credID)
	}

	return r.updateStatus(ctx, credential, credID, metav1.ConditionTrue, "Synced", "Credential synced successfully")
}

// getN8nClient creates an n8n API client from the credential spec
func (r *N8nCredentialReconciler) getN8nClient(ctx context.Context, credential *n8nv1alpha1.N8nCredential) (*n8n.Client, error) {
	// Get the API key from secret
	apiKeySecret := &corev1.Secret{}
	apiKeyRef := credential.Spec.N8nInstance.APIKeySecretRef
	namespace := apiKeyRef.Namespace
	if namespace == "" {
		namespace = credential.Namespace
	}

	if err := r.Get(ctx, types.NamespacedName{Name: apiKeyRef.Name, Namespace: namespace}, apiKeySecret); err != nil {
		return nil, fmt.Errorf("failed to get API key secret: %w", err)
	}

	apiKey, ok := apiKeySecret.Data[apiKeyRef.Key]
	if !ok {
		return nil, fmt.Errorf("API key not found in secret at key %s", apiKeyRef.Key)
	}

	// Determine the n8n URL
	var n8nURL string
	if credential.Spec.N8nInstance.URL != "" {
		n8nURL = credential.Spec.N8nInstance.URL
	} else if credential.Spec.N8nInstance.ServiceRef != nil {
		svcRef := credential.Spec.N8nInstance.ServiceRef
		namespace := svcRef.Namespace
		if namespace == "" {
			namespace = credential.Namespace
		}
		port := svcRef.Port
		if port == 0 {
			port = 5678
		}
		n8nURL = fmt.Sprintf("http://%s.%s.svc:%d", svcRef.Name, namespace, port)
	} else {
		return nil, fmt.Errorf("either URL or ServiceRef must be specified")
	}

	return n8n.NewClient(n8nURL, string(apiKey)), nil
}

// buildCredentialData builds the credential data map from Secret + static data
func (r *N8nCredentialReconciler) buildCredentialData(ctx context.Context, credential *n8nv1alpha1.N8nCredential) (map[string]interface{}, error) {
	data := make(map[string]interface{})

	// Start with static data
	for k, v := range credential.Spec.Data {
		data[k] = v
	}

	// Add data from Kubernetes Secret (can be managed by External Secrets)
	if credential.Spec.SecretRef != nil {
		secretData, err := r.getSecretData(ctx, credential)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret data: %w", err)
		}
		for k, v := range secretData {
			data[k] = v
		}
	}

	return data, nil
}

// getSecretData retrieves credential data from a Kubernetes Secret
func (r *N8nCredentialReconciler) getSecretData(ctx context.Context, credential *n8nv1alpha1.N8nCredential) (map[string]string, error) {
	secretRef := credential.Spec.SecretRef
	namespace := secretRef.Namespace
	if namespace == "" {
		namespace = credential.Namespace
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretRef.Name, Namespace: namespace}, secret); err != nil {
		return nil, err
	}

	result := make(map[string]string)
	fieldMappings := credential.Spec.FieldMappings

	for secretKey, secretValue := range secret.Data {
		// Check if there's a field mapping (secretKey -> credField)
		credField := secretKey
		if fieldMappings != nil {
			// FieldMappings: credField -> secretKey, so reverse lookup
			for cred, sec := range fieldMappings {
				if sec == secretKey {
					credField = cred
					break
				}
			}
		}
		result[credField] = string(secretValue)
	}

	return result, nil
}

// updateStatus updates the status of the N8nCredential resource
func (r *N8nCredentialReconciler) updateStatus(ctx context.Context, credential *n8nv1alpha1.N8nCredential, credID string, conditionStatus metav1.ConditionStatus, reason, message string) (ctrl.Result, error) {
	credential.Status.CredentialID = credID
	credential.Status.ObservedGeneration = credential.Generation
	now := metav1.Now()
	credential.Status.LastSyncTime = &now

	// Update condition
	condition := metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		ObservedGeneration: credential.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	}

	// Find and update or append the condition
	found := false
	for i, c := range credential.Status.Conditions {
		if c.Type == condition.Type {
			credential.Status.Conditions[i] = condition
			found = true
			break
		}
	}
	if !found {
		credential.Status.Conditions = append(credential.Status.Conditions, condition)
	}

	if err := r.Status().Update(ctx, credential); err != nil {
		log.FromContext(ctx).Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}

	// Requeue on failure
	if conditionStatus == metav1.ConditionFalse {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Requeue periodically to sync
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *N8nCredentialReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&n8nv1alpha1.N8nCredential{}).
		Named("n8ncredential").
		Complete(r)
}
