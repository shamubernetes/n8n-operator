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
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
	"github.com/shamubernetes/n8n-operator/pkg/n8n"
)

const (
	n8nCredentialFinalizer = "n8n.n8n.io/credential-finalizer"

	credentialSecretField = "n8ncredential.secretRef"
	credentialAPIKeyField = "n8ncredential.apiKeySecretRef"
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

	credential := &n8nv1alpha1.N8nCredential{}
	if err := r.Get(ctx, req.NamespacedName, credential); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("N8nCredential resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get N8nCredential")
		return ctrl.Result{}, err
	}

	if !credential.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, credential)
	}

	if !controllerutil.ContainsFinalizer(credential, n8nCredentialFinalizer) {
		controllerutil.AddFinalizer(credential, n8nCredentialFinalizer)
		if err := r.Update(ctx, credential); err != nil {
			return ctrl.Result{}, err
		}
	}

	n8nClient, err := r.getN8nClient(ctx, credential)
	if err != nil {
		logger.Error(err, "Failed to create n8n client")
		return r.updateStatus(ctx, credential, credential.Status.CredentialID, credential.Status.CredentialHash, metav1.ConditionFalse, "ClientError", err.Error())
	}

	credData, err := r.buildCredentialData(ctx, credential)
	if err != nil {
		logger.Error(err, "Failed to build credential data")
		return r.updateStatus(ctx, credential, credential.Status.CredentialID, credential.Status.CredentialHash, metav1.ConditionFalse, "DataError", err.Error())
	}

	credentialHash, err := hashCredentialPayload(credential.Spec.CredentialName, credential.Spec.CredentialType, credData)
	if err != nil {
		logger.Error(err, "Failed to hash credential payload")
		return r.updateStatus(ctx, credential, credential.Status.CredentialID, credential.Status.CredentialHash, metav1.ConditionFalse, "HashError", err.Error())
	}

	existingCred, err := n8nClient.GetCredentialByName(ctx, credential.Spec.CredentialName)
	if err != nil {
		logger.Error(err, "Failed to check existing credential")
		return r.updateStatus(ctx, credential, credential.Status.CredentialID, credentialHash, metav1.ConditionFalse, "APIError", err.Error())
	}

	var credID string
	if existingCred == nil {
		logger.Info("Creating new credential", "name", credential.Spec.CredentialName)
		newCred := &n8n.Credential{Name: credential.Spec.CredentialName, Type: credential.Spec.CredentialType, Data: credData}
		created, createErr := n8nClient.CreateCredential(ctx, newCred)
		if createErr != nil {
			logger.Error(createErr, "Failed to create credential")
			return r.updateStatus(ctx, credential, "", credentialHash, metav1.ConditionFalse, "CreateError", createErr.Error())
		}
		credID = created.ID
		logger.Info("Created credential", "id", credID)
	} else {
		credID = existingCred.ID
		needsUpdate := credential.Status.CredentialID != credID ||
			credential.Status.CredentialHash != credentialHash ||
			credential.Status.ObservedGeneration != credential.Generation
		if needsUpdate {
			logger.Info("Updating existing credential", "id", credID)
			updateCred := &n8n.Credential{Name: credential.Spec.CredentialName, Type: credential.Spec.CredentialType, Data: credData}
			if _, updateErr := n8nClient.UpdateCredential(ctx, credID, updateCred); updateErr != nil {
				logger.Error(updateErr, "Failed to update credential")
				return r.updateStatus(ctx, credential, credID, credentialHash, metav1.ConditionFalse, "UpdateError", updateErr.Error())
			}
			logger.Info("Updated credential", "id", credID)
		} else {
			logger.Info("Credential is already up to date", "id", credID)
		}
	}

	return r.updateStatus(ctx, credential, credID, credentialHash, metav1.ConditionTrue, "Synced", "Credential synced successfully")
}

func (r *N8nCredentialReconciler) handleDeletion(ctx context.Context, credential *n8nv1alpha1.N8nCredential) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(credential, n8nCredentialFinalizer) {
		return ctrl.Result{}, nil
	}

	if credential.Spec.DeletionPolicy == n8nv1alpha1.DeletionPolicyDelete {
		n8nClient, err := r.getN8nClient(ctx, credential)
		if err != nil {
			if !shouldForceFinalize(credential.DeletionTimestamp, err) {
				return ctrl.Result{}, err
			}
			log.FromContext(ctx).Error(err, "Credential cleanup failed, forcing finalizer removal", "name", credential.Name)
		} else {
			credID := credential.Status.CredentialID
			if credID == "" {
				existing, lookupErr := n8nClient.GetCredentialByName(ctx, credential.Spec.CredentialName)
				if lookupErr != nil {
					if !shouldForceFinalize(credential.DeletionTimestamp, lookupErr) {
						return ctrl.Result{}, lookupErr
					}
					log.FromContext(ctx).Error(lookupErr, "Credential lookup failed during delete, forcing finalizer removal", "name", credential.Name)
				} else if existing != nil {
					credID = existing.ID
				}
			}

			if credID != "" {
				if err := n8nClient.DeleteCredential(ctx, credID); err != nil && !isN8NNotFound(err) {
					if !shouldForceFinalize(credential.DeletionTimestamp, err) {
						return ctrl.Result{}, err
					}
					log.FromContext(ctx).Error(err, "Credential delete failed, forcing finalizer removal", "name", credential.Name, "credentialID", credID)
				}
			}
		}
	}

	controllerutil.RemoveFinalizer(credential, n8nCredentialFinalizer)
	if err := r.Update(ctx, credential); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// getN8nClient creates an n8n API client from the credential spec
func (r *N8nCredentialReconciler) getN8nClient(ctx context.Context, credential *n8nv1alpha1.N8nCredential) (*n8n.Client, error) {
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

	for k, v := range credential.Spec.Data {
		data[k] = parseCredentialValue(v)
	}

	if credential.Spec.SecretRef != nil {
		secretData, err := r.getSecretData(ctx, credential)
		if err != nil {
			return nil, fmt.Errorf("failed to get secret data: %w", err)
		}
		for k, v := range secretData {
			data[k] = parseCredentialValue(v)
		}
	}

	return data, nil
}

// parseCredentialValue attempts to parse a string value into its appropriate type.
// n8n API requires numeric fields (like port) to be actual numbers, not strings.
// This function converts:
//   - Integer strings ("5432") to int64
//   - Float strings ("3.14") to float64
//   - Boolean strings ("true"/"false") to bool
//   - Everything else remains a string
func parseCredentialValue(s string) interface{} {
	// Try parsing as integer first
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	// Try parsing as float
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	// Try parsing as boolean
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	// Return as string
	return s
}

func hashCredentialPayload(name, credType string, data map[string]interface{}) (string, error) {
	payload := struct {
		Name string                 `json:"name"`
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	}{
		Name: name,
		Type: credType,
		Data: data,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal credential payload: %w", err)
	}
	return hashContent(raw), nil
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
		credField := secretKey
		for cred, sec := range fieldMappings {
			if sec == secretKey {
				credField = cred
				break
			}
		}
		result[credField] = string(secretValue)
	}

	return result, nil
}

// updateStatus updates the status of the N8nCredential resource
func (r *N8nCredentialReconciler) updateStatus(ctx context.Context, credential *n8nv1alpha1.N8nCredential, credID, credentialHash string, conditionStatus metav1.ConditionStatus, reason, message string) (ctrl.Result, error) {
	result := ctrl.Result{RequeueAfter: 5 * time.Minute}
	if conditionStatus == metav1.ConditionFalse {
		result = ctrl.Result{RequeueAfter: 30 * time.Second}
	}

	original := credential.DeepCopy()

	credential.Status.CredentialID = credID
	credential.Status.CredentialHash = credentialHash
	credential.Status.ObservedGeneration = credential.Generation

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		ObservedGeneration: credential.Generation,
		Reason:             reason,
		Message:            message,
	}
	apimeta.SetStatusCondition(&credential.Status.Conditions, condition)

	if !apiequality.Semantic.DeepEqual(original.Status, credential.Status) {
		now := metav1.Now()
		credential.Status.LastSyncTime = &now
	}

	if apiequality.Semantic.DeepEqual(original.Status, credential.Status) {
		return result, nil
	}

	if err := r.Status().Update(ctx, credential); err != nil {
		log.FromContext(ctx).Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}

	return result, nil
}

func (r *N8nCredentialReconciler) indexCredentials(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &n8nv1alpha1.N8nCredential{}, credentialSecretField, func(obj client.Object) []string {
		credential := obj.(*n8nv1alpha1.N8nCredential)
		if credential.Spec.SecretRef == nil {
			return nil
		}
		return []string{namespacedKeyWithDefault(credential.Spec.SecretRef.Namespace, credential.Namespace, credential.Spec.SecretRef.Name)}
	}); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &n8nv1alpha1.N8nCredential{}, credentialAPIKeyField, func(obj client.Object) []string {
		credential := obj.(*n8nv1alpha1.N8nCredential)
		ref := credential.Spec.N8nInstance.APIKeySecretRef
		return []string{namespacedKeyWithDefault(ref.Namespace, credential.Namespace, ref.Name)}
	}); err != nil {
		return err
	}

	return nil
}

func (r *N8nCredentialReconciler) mapSecretToCredentials(ctx context.Context, obj client.Object) []reconcile.Request {
	secretKey := namespacedKey(obj.GetNamespace(), obj.GetName())
	requestSet := map[types.NamespacedName]struct{}{}

	for _, field := range []string{credentialSecretField, credentialAPIKeyField} {
		list := &n8nv1alpha1.N8nCredentialList{}
		if err := r.List(ctx, list, client.MatchingFields{field: secretKey}); err != nil {
			continue
		}
		for i := range list.Items {
			item := list.Items[i]
			requestSet[types.NamespacedName{Name: item.Name, Namespace: item.Namespace}] = struct{}{}
		}
	}

	requests := make([]reconcile.Request, 0, len(requestSet))
	for nn := range requestSet {
		requests = append(requests, reconcile.Request{NamespacedName: nn})
	}
	return requests
}

func namespacedKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

func namespacedKeyWithDefault(namespace, defaultNamespace, name string) string {
	if namespace == "" {
		namespace = defaultNamespace
	}
	return namespacedKey(namespace, name)
}

// SetupWithManager sets up the controller with the Manager.
func (r *N8nCredentialReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := r.indexCredentials(context.Background(), mgr); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&n8nv1alpha1.N8nCredential{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapSecretToCredentials)).
		Named("n8ncredential").
		Complete(r)
}
