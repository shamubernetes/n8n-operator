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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	n8nWorkflowFinalizer = "n8n.n8n.io/workflow-finalizer"

	workflowSourceConfigMapField = "n8nworkflow.sourceConfigMap"
	workflowSourceSecretField    = "n8nworkflow.sourceSecret"
	workflowAPIKeyField          = "n8nworkflow.apiKeySecretRef"
)

// N8nWorkflowReconciler reconciles a N8nWorkflow object
type N8nWorkflowReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8nworkflows,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8nworkflows/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8nworkflows/finalizers,verbs=update
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ncredentials,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// Reconcile handles the reconciliation of N8nWorkflow resources
func (r *N8nWorkflowReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	workflow := &n8nv1alpha1.N8nWorkflow{}
	if err := r.Get(ctx, req.NamespacedName, workflow); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("N8nWorkflow resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get N8nWorkflow")
		return ctrl.Result{}, err
	}

	if !workflow.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, workflow)
	}

	if !controllerutil.ContainsFinalizer(workflow, n8nWorkflowFinalizer) {
		controllerutil.AddFinalizer(workflow, n8nWorkflowFinalizer)
		if err := r.Update(ctx, workflow); err != nil {
			return ctrl.Result{}, err
		}
	}

	n8nClient, err := r.getN8nClient(ctx, workflow)
	if err != nil {
		logger.Error(err, "Failed to create n8n client")
		return r.updateStatus(ctx, workflow, workflow.Status.WorkflowID, workflow.Status.Active, workflow.Status.WorkflowHash, metav1.ConditionFalse, "ClientError", err.Error())
	}

	workflowJSON, err := r.getWorkflowJSON(ctx, workflow)
	if err != nil {
		logger.Error(err, "Failed to get workflow JSON")
		return r.updateStatus(ctx, workflow, workflow.Status.WorkflowID, workflow.Status.Active, workflow.Status.WorkflowHash, metav1.ConditionFalse, "JSONError", err.Error())
	}

	var workflowData map[string]interface{}
	if err := json.Unmarshal(workflowJSON, &workflowData); err != nil {
		logger.Error(err, "Failed to parse workflow JSON")
		return r.updateStatus(ctx, workflow, workflow.Status.WorkflowID, workflow.Status.Active, workflow.Status.WorkflowHash, metav1.ConditionFalse, "ParseError", err.Error())
	}

	workflowData["name"] = workflow.Spec.WorkflowName

	delete(workflowData, "id")
	delete(workflowData, "versionId")
	delete(workflowData, "createdAt")
	delete(workflowData, "updatedAt")
	delete(workflowData, "active")

	if len(workflow.Spec.CredentialMappings) > 0 {
		if err := r.updateCredentialIDs(ctx, workflow, workflowData); err != nil {
			logger.Error(err, "Failed to update credential IDs")
			return r.updateStatus(ctx, workflow, workflow.Status.WorkflowID, workflow.Status.Active, workflow.Status.WorkflowHash, metav1.ConditionFalse, "CredentialError", err.Error())
		}
	}

	desiredWorkflow := buildWorkflowPayload(workflow.Spec.WorkflowName, workflowData)
	workflowHash, err := hashWorkflowPayload(desiredWorkflow)
	if err != nil {
		logger.Error(err, "Failed to hash workflow payload")
		return r.updateStatus(ctx, workflow, workflow.Status.WorkflowID, workflow.Status.Active, workflow.Status.WorkflowHash, metav1.ConditionFalse, "HashError", err.Error())
	}

	existingWf, err := n8nClient.GetWorkflowByName(ctx, workflow.Spec.WorkflowName)
	if err != nil {
		logger.Error(err, "Failed to check existing workflow")
		return r.updateStatus(ctx, workflow, workflow.Status.WorkflowID, workflow.Status.Active, workflowHash, metav1.ConditionFalse, "APIError", err.Error())
	}

	var wfID string
	var isActive bool

	if existingWf == nil {
		logger.Info("Creating new workflow", "name", workflow.Spec.WorkflowName)
		created, createErr := n8nClient.CreateWorkflow(ctx, desiredWorkflow)
		if createErr != nil {
			logger.Error(createErr, "Failed to create workflow")
			return r.updateStatus(ctx, workflow, "", false, workflowHash, metav1.ConditionFalse, "CreateError", createErr.Error())
		}
		wfID = created.ID
		isActive = created.Active
		logger.Info("Created workflow", "id", wfID)
	} else {
		wfID = existingWf.ID
		isActive = existingWf.Active
		needsUpdate := workflow.Status.WorkflowID != wfID ||
			workflow.Status.WorkflowHash != workflowHash ||
			workflow.Status.ObservedGeneration != workflow.Generation
		if needsUpdate {
			logger.Info("Updating existing workflow", "id", wfID)
			updated, updateErr := n8nClient.UpdateWorkflow(ctx, wfID, desiredWorkflow)
			if updateErr != nil {
				logger.Error(updateErr, "Failed to update workflow")
				return r.updateStatus(ctx, workflow, wfID, existingWf.Active, workflowHash, metav1.ConditionFalse, "UpdateError", updateErr.Error())
			}
			isActive = updated.Active
			logger.Info("Updated workflow", "id", wfID)
		} else {
			logger.Info("Workflow is already up to date", "id", wfID)
		}
	}

	if workflow.Spec.Active && !isActive {
		logger.Info("Activating workflow", "id", wfID)
		activated, actErr := n8nClient.ActivateWorkflow(ctx, wfID)
		if actErr != nil {
			logger.Error(actErr, "Failed to activate workflow")
			return r.updateStatus(ctx, workflow, wfID, false, workflowHash, metav1.ConditionFalse, "ActivationError", actErr.Error())
		}
		isActive = activated.Active
	} else if !workflow.Spec.Active && isActive {
		logger.Info("Deactivating workflow", "id", wfID)
		deactivated, deactErr := n8nClient.DeactivateWorkflow(ctx, wfID)
		if deactErr != nil {
			logger.Error(deactErr, "Failed to deactivate workflow")
			return r.updateStatus(ctx, workflow, wfID, true, workflowHash, metav1.ConditionFalse, "DeactivationError", deactErr.Error())
		}
		isActive = deactivated.Active
	}

	return r.updateStatus(ctx, workflow, wfID, isActive, workflowHash, metav1.ConditionTrue, "Synced", "Workflow synced successfully")
}

func buildWorkflowPayload(name string, workflowData map[string]interface{}) *n8n.Workflow {
	wf := &n8n.Workflow{Name: name}
	if nodes, ok := workflowData["nodes"].([]interface{}); ok {
		wf.Nodes = make([]map[string]interface{}, len(nodes))
		for i, n := range nodes {
			if nodeMap, ok := n.(map[string]interface{}); ok {
				wf.Nodes[i] = nodeMap
			}
		}
	}
	if connections, ok := workflowData["connections"].(map[string]interface{}); ok {
		wf.Connections = connections
	}
	if settings, ok := workflowData["settings"].(map[string]interface{}); ok {
		wf.Settings = settings
	}
	return wf
}

func (r *N8nWorkflowReconciler) handleDeletion(ctx context.Context, workflow *n8nv1alpha1.N8nWorkflow) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(workflow, n8nWorkflowFinalizer) {
		return ctrl.Result{}, nil
	}

	if workflow.Spec.DeletionPolicy == n8nv1alpha1.DeletionPolicyDelete {
		n8nClient, err := r.getN8nClient(ctx, workflow)
		if err != nil {
			if !shouldForceFinalize(workflow.DeletionTimestamp, err) {
				return ctrl.Result{}, err
			}
			log.FromContext(ctx).Error(err, "Workflow cleanup failed, forcing finalizer removal", "name", workflow.Name)
		} else {
			wfID := workflow.Status.WorkflowID
			if wfID == "" {
				existing, lookupErr := n8nClient.GetWorkflowByName(ctx, workflow.Spec.WorkflowName)
				if lookupErr != nil {
					if !shouldForceFinalize(workflow.DeletionTimestamp, lookupErr) {
						return ctrl.Result{}, lookupErr
					}
					log.FromContext(ctx).Error(lookupErr, "Workflow lookup failed during delete, forcing finalizer removal", "name", workflow.Name)
				} else if existing != nil {
					wfID = existing.ID
				}
			}

			if wfID != "" {
				if err := n8nClient.DeleteWorkflow(ctx, wfID); err != nil && !isN8NNotFound(err) {
					if !shouldForceFinalize(workflow.DeletionTimestamp, err) {
						return ctrl.Result{}, err
					}
					log.FromContext(ctx).Error(err, "Workflow delete failed, forcing finalizer removal", "name", workflow.Name, "workflowID", wfID)
				}
			}
		}
	}

	controllerutil.RemoveFinalizer(workflow, n8nWorkflowFinalizer)
	if err := r.Update(ctx, workflow); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// getN8nClient creates an n8n API client from the workflow spec
func (r *N8nWorkflowReconciler) getN8nClient(ctx context.Context, workflow *n8nv1alpha1.N8nWorkflow) (*n8n.Client, error) {
	apiKeySecret := &corev1.Secret{}
	apiKeyRef := workflow.Spec.N8nInstance.APIKeySecretRef
	namespace := apiKeyRef.Namespace
	if namespace == "" {
		namespace = workflow.Namespace
	}

	if err := r.Get(ctx, types.NamespacedName{Name: apiKeyRef.Name, Namespace: namespace}, apiKeySecret); err != nil {
		return nil, fmt.Errorf("failed to get API key secret: %w", err)
	}

	apiKey, ok := apiKeySecret.Data[apiKeyRef.Key]
	if !ok {
		return nil, fmt.Errorf("API key not found in secret at key %s", apiKeyRef.Key)
	}

	var n8nURL string
	if workflow.Spec.N8nInstance.URL != "" {
		n8nURL = workflow.Spec.N8nInstance.URL
	} else if workflow.Spec.N8nInstance.ServiceRef != nil {
		svcRef := workflow.Spec.N8nInstance.ServiceRef
		namespace := svcRef.Namespace
		if namespace == "" {
			namespace = workflow.Namespace
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

// getWorkflowJSON retrieves the workflow JSON from the configured source
func (r *N8nWorkflowReconciler) getWorkflowJSON(ctx context.Context, workflow *n8nv1alpha1.N8nWorkflow) ([]byte, error) {
	if workflow.Spec.Workflow != nil {
		return workflow.Spec.Workflow.Raw, nil
	}

	if workflow.Spec.SourceRef == nil {
		return nil, fmt.Errorf("either workflow or sourceRef must be specified")
	}

	sourceRef := workflow.Spec.SourceRef
	namespace := sourceRef.Namespace
	if namespace == "" {
		namespace = workflow.Namespace
	}

	switch sourceRef.Kind {
	case "ConfigMap", "":
		cm := &corev1.ConfigMap{}
		if err := r.Get(ctx, types.NamespacedName{Name: sourceRef.Name, Namespace: namespace}, cm); err != nil {
			return nil, fmt.Errorf("failed to get ConfigMap: %w", err)
		}
		data, ok := cm.Data[sourceRef.Key]
		if !ok {
			return nil, fmt.Errorf("key %s not found in ConfigMap", sourceRef.Key)
		}
		return []byte(data), nil

	case "Secret":
		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: sourceRef.Name, Namespace: namespace}, secret); err != nil {
			return nil, fmt.Errorf("failed to get Secret: %w", err)
		}
		data, ok := secret.Data[sourceRef.Key]
		if !ok {
			return nil, fmt.Errorf("key %s not found in Secret", sourceRef.Key)
		}
		return data, nil

	default:
		return nil, fmt.Errorf("unsupported source kind: %s", sourceRef.Kind)
	}
}

// updateCredentialIDs updates credential references in the workflow to use actual n8n credential IDs
func (r *N8nWorkflowReconciler) updateCredentialIDs(ctx context.Context, workflow *n8nv1alpha1.N8nWorkflow, workflowData map[string]interface{}) error {
	credentialIDs := make(map[string]string)
	for credName, credResource := range workflow.Spec.CredentialMappings {
		cred := &n8nv1alpha1.N8nCredential{}
		if err := r.Get(ctx, types.NamespacedName{Name: credResource, Namespace: workflow.Namespace}, cred); err != nil {
			return fmt.Errorf("failed to get N8nCredential %s: %w", credResource, err)
		}
		if cred.Status.CredentialID == "" {
			return fmt.Errorf("N8nCredential %s has no credential ID (not yet synced?)", credResource)
		}
		credentialIDs[credName] = cred.Status.CredentialID
	}

	nodes, ok := workflowData["nodes"].([]interface{})
	if !ok {
		return nil
	}

	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

		credentials, ok := nodeMap["credentials"].(map[string]interface{})
		if !ok {
			continue
		}

		for _, credRef := range credentials {
			credRefMap, ok := credRef.(map[string]interface{})
			if !ok {
				continue
			}

			credName, ok := credRefMap["name"].(string)
			if !ok {
				continue
			}

			if newID, ok := credentialIDs[credName]; ok {
				credRefMap["id"] = newID
			}
		}
	}

	return nil
}

// hashContent creates a SHA256 hash of the content
func hashContent(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

func hashWorkflowPayload(wf *n8n.Workflow) (string, error) {
	content, err := json.Marshal(wf)
	if err != nil {
		return "", fmt.Errorf("failed to marshal workflow payload: %w", err)
	}
	return hashContent(content), nil
}

// updateStatus updates the status of the N8nWorkflow resource
func (r *N8nWorkflowReconciler) updateStatus(ctx context.Context, workflow *n8nv1alpha1.N8nWorkflow, wfID string, active bool, hash string, conditionStatus metav1.ConditionStatus, reason, message string) (ctrl.Result, error) {
	result := ctrl.Result{RequeueAfter: 5 * time.Minute}
	if conditionStatus == metav1.ConditionFalse {
		result = ctrl.Result{RequeueAfter: 30 * time.Second}
	}

	original := workflow.DeepCopy()

	workflow.Status.WorkflowID = wfID
	workflow.Status.Active = active
	workflow.Status.WorkflowHash = hash
	workflow.Status.ObservedGeneration = workflow.Generation

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		ObservedGeneration: workflow.Generation,
		Reason:             reason,
		Message:            message,
	}
	apimeta.SetStatusCondition(&workflow.Status.Conditions, condition)

	if !apiequality.Semantic.DeepEqual(original.Status, workflow.Status) {
		now := metav1.Now()
		workflow.Status.LastSyncTime = &now
	}

	if apiequality.Semantic.DeepEqual(original.Status, workflow.Status) {
		return result, nil
	}

	if err := r.Status().Update(ctx, workflow); err != nil {
		log.FromContext(ctx).Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}

	return result, nil
}

func (r *N8nWorkflowReconciler) indexWorkflows(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &n8nv1alpha1.N8nWorkflow{}, workflowSourceConfigMapField, func(obj client.Object) []string {
		workflow := obj.(*n8nv1alpha1.N8nWorkflow)
		if workflow.Spec.SourceRef == nil {
			return nil
		}
		if workflow.Spec.SourceRef.Kind != "" && workflow.Spec.SourceRef.Kind != "ConfigMap" {
			return nil
		}
		ns := workflow.Spec.SourceRef.Namespace
		if ns == "" {
			ns = workflow.Namespace
		}
		return []string{namespacedKey(ns, workflow.Spec.SourceRef.Name)}
	}); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &n8nv1alpha1.N8nWorkflow{}, workflowSourceSecretField, func(obj client.Object) []string {
		workflow := obj.(*n8nv1alpha1.N8nWorkflow)
		if workflow.Spec.SourceRef == nil || workflow.Spec.SourceRef.Kind != "Secret" {
			return nil
		}
		ns := workflow.Spec.SourceRef.Namespace
		if ns == "" {
			ns = workflow.Namespace
		}
		return []string{namespacedKey(ns, workflow.Spec.SourceRef.Name)}
	}); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &n8nv1alpha1.N8nWorkflow{}, workflowAPIKeyField, func(obj client.Object) []string {
		workflow := obj.(*n8nv1alpha1.N8nWorkflow)
		ref := workflow.Spec.N8nInstance.APIKeySecretRef
		return []string{namespacedKeyWithDefault(ref.Namespace, workflow.Namespace, ref.Name)}
	}); err != nil {
		return err
	}

	return nil
}

func (r *N8nWorkflowReconciler) mapConfigMapToWorkflows(ctx context.Context, obj client.Object) []reconcile.Request {
	key := namespacedKey(obj.GetNamespace(), obj.GetName())
	list := &n8nv1alpha1.N8nWorkflowList{}
	if err := r.List(ctx, list, client.MatchingFields{workflowSourceConfigMapField: key}); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		item := list.Items[i]
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: item.Name, Namespace: item.Namespace}})
	}
	return requests
}

func (r *N8nWorkflowReconciler) mapSecretToWorkflows(ctx context.Context, obj client.Object) []reconcile.Request {
	key := namespacedKey(obj.GetNamespace(), obj.GetName())
	requestSet := map[types.NamespacedName]struct{}{}

	for _, field := range []string{workflowSourceSecretField, workflowAPIKeyField} {
		list := &n8nv1alpha1.N8nWorkflowList{}
		if err := r.List(ctx, list, client.MatchingFields{field: key}); err != nil {
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

func (r *N8nWorkflowReconciler) mapCredentialToWorkflows(ctx context.Context, obj client.Object) []reconcile.Request {
	credential, ok := obj.(*n8nv1alpha1.N8nCredential)
	if !ok {
		return nil
	}

	list := &n8nv1alpha1.N8nWorkflowList{}
	if err := r.List(ctx, list, client.InNamespace(credential.Namespace)); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0)
	for i := range list.Items {
		wf := list.Items[i]
		for _, mappedCredential := range wf.Spec.CredentialMappings {
			if mappedCredential == credential.Name {
				requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: wf.Name, Namespace: wf.Namespace}})
				break
			}
		}
	}

	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *N8nWorkflowReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := r.indexWorkflows(context.Background(), mgr); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&n8nv1alpha1.N8nWorkflow{}).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.mapConfigMapToWorkflows)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapSecretToWorkflows)).
		Watches(&n8nv1alpha1.N8nCredential{}, handler.EnqueueRequestsFromMapFunc(r.mapCredentialToWorkflows)).
		Named("n8nworkflow").
		Complete(r)
}
