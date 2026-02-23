package controller

import (
	"context"
	"fmt"
	"strings"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

// N8nPluginReconciler reconciles a N8nPlugin object
type N8nPluginReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8nplugins,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8nplugins/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8nplugins/finalizers,verbs=update
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ninstances,verbs=get;list;watch

// Reconcile handles the reconciliation of N8nPlugin resources.
func (r *N8nPluginReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	plugin := &n8nv1alpha1.N8nPlugin{}
	if err := r.Get(ctx, req.NamespacedName, plugin); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("N8nPlugin resource not found, ignoring")
			if req.Namespace != "" {
				if syncErr := r.reconcileNamespacePluginStatuses(ctx, req.Namespace); syncErr != nil {
					logger.Error(syncErr, "Failed to refresh N8nPlugin statuses after delete")
					return ctrl.Result{}, syncErr
				}
			}
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get N8nPlugin")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.reconcileNamespacePluginStatuses(ctx, plugin.Namespace)
}

// SetupWithManager sets up the controller with the Manager.
func (r *N8nPluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&n8nv1alpha1.N8nPlugin{}).
		Watches(&n8nv1alpha1.N8nInstance{}, handler.EnqueueRequestsFromMapFunc(r.mapInstanceToPlugins)).
		Named("n8nplugin").
		Complete(r)
}

func (r *N8nPluginReconciler) mapInstanceToPlugins(ctx context.Context, obj client.Object) []reconcile.Request {
	instance, ok := obj.(*n8nv1alpha1.N8nInstance)
	if !ok {
		return nil
	}

	pluginList := &n8nv1alpha1.N8nPluginList{}
	if err := r.List(ctx, pluginList, client.InNamespace(instance.Namespace)); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(pluginList.Items))
	for i := range pluginList.Items {
		plugin := pluginList.Items[i]
		if strings.TrimSpace(plugin.Spec.InstanceRef.Name) != instance.Name {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: plugin.Namespace,
				Name:      plugin.Name,
			},
		})
	}

	return requests
}

func (r *N8nPluginReconciler) reconcileNamespacePluginStatuses(ctx context.Context, namespace string) error {
	pluginList := &n8nv1alpha1.N8nPluginList{}
	if err := r.List(ctx, pluginList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list N8nPlugin resources: %w", err)
	}

	pluginsByInstance := make(map[string][]*n8nv1alpha1.N8nPlugin)
	for i := range pluginList.Items {
		plugin := &pluginList.Items[i]
		instanceName := strings.TrimSpace(plugin.Spec.InstanceRef.Name)
		pluginsByInstance[instanceName] = append(pluginsByInstance[instanceName], plugin)
	}

	instanceExists := make(map[string]bool)
	instancePlanErr := make(map[string]error)
	for instanceName, plugins := range pluginsByInstance {
		if instanceName == "" {
			continue
		}

		instance := &n8nv1alpha1.N8nInstance{}
		key := types.NamespacedName{Name: instanceName, Namespace: namespace}
		if err := r.Get(ctx, key, instance); err != nil {
			if errors.IsNotFound(err) {
				instanceExists[instanceName] = false
				continue
			}
			return fmt.Errorf("failed to get N8nInstance %q: %w", instanceName, err)
		}
		instanceExists[instanceName] = true

		group := make([]n8nv1alpha1.N8nPlugin, 0, len(plugins))
		for i := range plugins {
			group = append(group, *plugins[i])
		}
		_, err := buildPluginInstallPlan(group)
		instancePlanErr[instanceName] = err
	}

	for _, plugins := range pluginsByInstance {
		for i := range plugins {
			plugin := plugins[i]
			changed, err := r.applyPluginStatus(plugin, instanceExists, instancePlanErr)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			if err := r.Status().Update(ctx, plugin); err != nil {
				if errors.IsConflict(err) {
					return err
				}
				return fmt.Errorf("failed to update N8nPlugin status for %s/%s: %w", plugin.Namespace, plugin.Name, err)
			}
		}
	}

	return nil
}

func (r *N8nPluginReconciler) applyPluginStatus(
	plugin *n8nv1alpha1.N8nPlugin,
	instanceExists map[string]bool,
	instancePlanErr map[string]error,
) (bool, error) {
	readyStatus := metav1.ConditionFalse
	reason := "Disabled"
	message := "Plugin is disabled"

	instanceName := strings.TrimSpace(plugin.Spec.InstanceRef.Name)
	if plugin.Spec.Enabled == nil || *plugin.Spec.Enabled {
		switch {
		case instanceName == "":
			reason = "InvalidSpec"
			message = "spec.instanceRef.name is required"
		case !instanceExists[instanceName]:
			reason = "InstanceNotFound"
			message = fmt.Sprintf("Target N8nInstance %q was not found", instanceName)
		case instancePlanErr[instanceName] != nil:
			reason = "InvalidPluginSet"
			message = instancePlanErr[instanceName].Error()
		default:
			readyStatus = metav1.ConditionTrue
			reason = "Bound"
			message = "Plugin is ready for installation"
		}
	}

	updatedStatus := plugin.Status.DeepCopy()
	updatedStatus.ResolvedPackage = resolvePluginPackageSpec(plugin.Spec)
	updatedStatus.ObservedGeneration = plugin.Generation
	apimeta.SetStatusCondition(&updatedStatus.Conditions, metav1.Condition{
		Type:               pluginReadyConditionType,
		Status:             readyStatus,
		ObservedGeneration: plugin.Generation,
		Reason:             reason,
		Message:            message,
	})

	if apiequality.Semantic.DeepEqual(plugin.Status, *updatedStatus) {
		return false, nil
	}

	plugin.Status = *updatedStatus
	return true, nil
}
