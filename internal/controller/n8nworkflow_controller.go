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

// N8nWorkflowReconciler reconciles a N8nWorkflow object
type N8nWorkflowReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8nworkflows,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8nworkflows/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8nworkflows/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// Reconcile handles the reconciliation of N8nWorkflow resources
func (r *N8nWorkflowReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the N8nWorkflow instance
	workflow := &n8nv1alpha1.N8nWorkflow{}
	if err := r.Get(ctx, req.NamespacedName, workflow); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("N8nWorkflow resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get N8nWorkflow")
		return ctrl.Result{}, err
	}

	// Get the n8n API client
	n8nClient, err := r.getN8nClient(ctx, workflow)
	if err != nil {
		logger.Error(err, "Failed to create n8n client")
		return r.updateStatus(ctx, workflow, "", false, "", metav1.ConditionFalse, "ClientError", err.Error())
	}

	// Get the workflow JSON
	workflowJSON, err := r.getWorkflowJSON(ctx, workflow)
	if err != nil {
		logger.Error(err, "Failed to get workflow JSON")
		return r.updateStatus(ctx, workflow, "", false, "", metav1.ConditionFalse, "JSONError", err.Error())
	}

	// Calculate hash of workflow content
	workflowHash := hashContent(workflowJSON)

	// Parse the workflow JSON
	var workflowData map[string]interface{}
	if err := json.Unmarshal(workflowJSON, &workflowData); err != nil {
		logger.Error(err, "Failed to parse workflow JSON")
		return r.updateStatus(ctx, workflow, "", false, "", metav1.ConditionFalse, "ParseError", err.Error())
	}

	// Set the workflow name from spec
	workflowData["name"] = workflow.Spec.WorkflowName

	// Remove fields that shouldn't be in the request
	delete(workflowData, "id")
	delete(workflowData, "versionId")
	delete(workflowData, "createdAt")
	delete(workflowData, "updatedAt")
	delete(workflowData, "active") // Active is set separately

	// Update credential IDs in the workflow if mappings are provided
	if len(workflow.Spec.CredentialMappings) > 0 {
		if err := r.updateCredentialIDs(ctx, workflow, workflowData); err != nil {
			logger.Error(err, "Failed to update credential IDs")
			return r.updateStatus(ctx, workflow, "", false, workflowHash, metav1.ConditionFalse, "CredentialError", err.Error())
		}
	}

	// Check if workflow exists in n8n
	existingWf, err := n8nClient.GetWorkflowByName(ctx, workflow.Spec.WorkflowName)
	if err != nil {
		logger.Error(err, "Failed to check existing workflow")
		return r.updateStatus(ctx, workflow, "", false, workflowHash, metav1.ConditionFalse, "APIError", err.Error())
	}

	var wfID string
	var isActive bool

	if existingWf == nil {
		// Create new workflow
		logger.Info("Creating new workflow", "name", workflow.Spec.WorkflowName)

		newWf := &n8n.Workflow{
			Name: workflow.Spec.WorkflowName,
		}
		// Copy nodes, connections, settings from parsed data
		if nodes, ok := workflowData["nodes"].([]interface{}); ok {
			newWf.Nodes = make([]map[string]interface{}, len(nodes))
			for i, n := range nodes {
				if nodeMap, ok := n.(map[string]interface{}); ok {
					newWf.Nodes[i] = nodeMap
				}
			}
		}
		if connections, ok := workflowData["connections"].(map[string]interface{}); ok {
			newWf.Connections = connections
		}
		if settings, ok := workflowData["settings"].(map[string]interface{}); ok {
			newWf.Settings = settings
		}

		created, err := n8nClient.CreateWorkflow(ctx, newWf)
		if err != nil {
			logger.Error(err, "Failed to create workflow")
			return r.updateStatus(ctx, workflow, "", false, workflowHash, metav1.ConditionFalse, "CreateError", err.Error())
		}
		wfID = created.ID
		isActive = created.Active
		logger.Info("Created workflow", "id", wfID)
	} else {
		// Update existing workflow
		wfID = existingWf.ID
		logger.Info("Updating existing workflow", "id", wfID)

		updateWf := &n8n.Workflow{
			Name: workflow.Spec.WorkflowName,
		}
		// Copy nodes, connections, settings from parsed data
		if nodes, ok := workflowData["nodes"].([]interface{}); ok {
			updateWf.Nodes = make([]map[string]interface{}, len(nodes))
			for i, n := range nodes {
				if nodeMap, ok := n.(map[string]interface{}); ok {
					updateWf.Nodes[i] = nodeMap
				}
			}
		}
		if connections, ok := workflowData["connections"].(map[string]interface{}); ok {
			updateWf.Connections = connections
		}
		if settings, ok := workflowData["settings"].(map[string]interface{}); ok {
			updateWf.Settings = settings
		}

		updated, err := n8nClient.UpdateWorkflow(ctx, wfID, updateWf)
		if err != nil {
			logger.Error(err, "Failed to update workflow")
			return r.updateStatus(ctx, workflow, wfID, existingWf.Active, workflowHash, metav1.ConditionFalse, "UpdateError", err.Error())
		}
		isActive = updated.Active
		logger.Info("Updated workflow", "id", wfID)
	}

	// Handle activation state
	if workflow.Spec.Active && !isActive {
		logger.Info("Activating workflow", "id", wfID)
		activated, err := n8nClient.ActivateWorkflow(ctx, wfID)
		if err != nil {
			logger.Error(err, "Failed to activate workflow")
			return r.updateStatus(ctx, workflow, wfID, false, workflowHash, metav1.ConditionFalse, "ActivationError", err.Error())
		}
		isActive = activated.Active
	} else if !workflow.Spec.Active && isActive {
		logger.Info("Deactivating workflow", "id", wfID)
		deactivated, err := n8nClient.DeactivateWorkflow(ctx, wfID)
		if err != nil {
			logger.Error(err, "Failed to deactivate workflow")
			return r.updateStatus(ctx, workflow, wfID, true, workflowHash, metav1.ConditionFalse, "DeactivationError", err.Error())
		}
		isActive = deactivated.Active
	}

	return r.updateStatus(ctx, workflow, wfID, isActive, workflowHash, metav1.ConditionTrue, "Synced", "Workflow synced successfully")
}

// getN8nClient creates an n8n API client from the workflow spec
func (r *N8nWorkflowReconciler) getN8nClient(ctx context.Context, workflow *n8nv1alpha1.N8nWorkflow) (*n8n.Client, error) {
	// Get the API key from secret
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

	// Determine the n8n URL
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
	// Check for inline workflow first
	if workflow.Spec.Workflow != nil {
		return workflow.Spec.Workflow.Raw, nil
	}

	// Get from ConfigMap or Secret
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
	// Get credential IDs from N8nCredential resources
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

	// Update nodes with credential references
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

			// Check if we have a mapping for this credential
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

// updateStatus updates the status of the N8nWorkflow resource
func (r *N8nWorkflowReconciler) updateStatus(ctx context.Context, workflow *n8nv1alpha1.N8nWorkflow, wfID string, active bool, hash string, conditionStatus metav1.ConditionStatus, reason, message string) (ctrl.Result, error) {
	workflow.Status.WorkflowID = wfID
	workflow.Status.Active = active
	workflow.Status.WorkflowHash = hash
	workflow.Status.ObservedGeneration = workflow.Generation
	now := metav1.Now()
	workflow.Status.LastSyncTime = &now

	// Update condition
	condition := metav1.Condition{
		Type:               "Ready",
		Status:             conditionStatus,
		ObservedGeneration: workflow.Generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	}

	// Find and update or append the condition
	found := false
	for i, c := range workflow.Status.Conditions {
		if c.Type == condition.Type {
			workflow.Status.Conditions[i] = condition
			found = true
			break
		}
	}
	if !found {
		workflow.Status.Conditions = append(workflow.Status.Conditions, condition)
	}

	if err := r.Status().Update(ctx, workflow); err != nil {
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
func (r *N8nWorkflowReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&n8nv1alpha1.N8nWorkflow{}).
		Named("n8nworkflow").
		Complete(r)
}
