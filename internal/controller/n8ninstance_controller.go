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
	"strings"
	"time"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

const (
	n8nFinalizer = "n8n.n8n.io/finalizer"

	n8nComponentMain   = "main"
	n8nComponentWorker = "worker"
)

// N8nInstanceReconciler reconciles a N8nInstance object
type N8nInstanceReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ninstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ninstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ninstances/finalizers,verbs=update
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ninstances/scale,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles the reconciliation of N8nInstance resources
func (r *N8nInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	instance := &n8nv1alpha1.N8nInstance{}
	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("N8nInstance resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get N8nInstance")
		return ctrl.Result{}, err
	}

	if !instance.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, instance)
	}

	if !controllerutil.ContainsFinalizer(instance, n8nFinalizer) {
		controllerutil.AddFinalizer(instance, n8nFinalizer)
		if err := r.Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(instance, corev1.EventTypeNormal, "Finalizer", "Added finalizer")
	}

	r.setDefaults(instance)

	if instance.Spec.Persistence != nil && (instance.Spec.Persistence.Enabled == nil || *instance.Spec.Persistence.Enabled) {
		if err := r.reconcilePVC(ctx, instance); err != nil {
			logger.Error(err, "Failed to reconcile PVC")
			r.Recorder.Event(instance, corev1.EventTypeWarning, "PVCFailed", err.Error())
			return r.updateStatus(ctx, instance, "Failed", err.Error())
		}
	}

	if err := r.reconcileService(ctx, instance); err != nil {
		logger.Error(err, "Failed to reconcile Service")
		r.Recorder.Event(instance, corev1.EventTypeWarning, "ServiceFailed", err.Error())
		return r.updateStatus(ctx, instance, "Failed", err.Error())
	}

	mainUpdated, err := r.reconcileDeployment(ctx, instance, n8nComponentMain, instance.Name, r.desiredMainReplicas(instance))
	if err != nil {
		logger.Error(err, "Failed to reconcile main Deployment")
		r.Recorder.Event(instance, corev1.EventTypeWarning, "DeploymentFailed", err.Error())
		return r.updateStatus(ctx, instance, "Failed", err.Error())
	}
	if mainUpdated {
		r.Recorder.Event(instance, corev1.EventTypeNormal, "DeploymentUpdated", "Updated main Deployment")
	}

	if r.queueWorkerEnabled(instance) {
		workerUpdated, workerErr := r.reconcileDeployment(ctx, instance, n8nComponentWorker, workerDeploymentName(instance), r.desiredWorkerReplicas(instance))
		if workerErr != nil {
			logger.Error(workerErr, "Failed to reconcile worker Deployment")
			r.Recorder.Event(instance, corev1.EventTypeWarning, "WorkerDeploymentFailed", workerErr.Error())
			return r.updateStatus(ctx, instance, "Failed", workerErr.Error())
		}
		if workerUpdated {
			r.Recorder.Event(instance, corev1.EventTypeNormal, "WorkerDeploymentUpdated", "Updated worker Deployment")
		}
	} else {
		if err := r.deleteDeploymentIfExists(ctx, instance.Namespace, workerDeploymentName(instance)); err != nil {
			logger.Error(err, "Failed to delete worker Deployment")
			return r.updateStatus(ctx, instance, "Failed", err.Error())
		}
	}

	if r.serviceMonitorEnabled(instance) {
		if err := r.reconcileServiceMonitor(ctx, instance); err != nil {
			if isServiceMonitorUnavailable(err) {
				logger.Info("ServiceMonitor CRD not available, skipping ServiceMonitor reconciliation")
			} else {
				logger.Error(err, "Failed to reconcile ServiceMonitor")
				r.Recorder.Event(instance, corev1.EventTypeWarning, "ServiceMonitorFailed", err.Error())
				return r.updateStatus(ctx, instance, "Failed", err.Error())
			}
		}
	} else {
		if err := r.deleteServiceMonitorIfExists(ctx, instance); err != nil {
			if isServiceMonitorUnavailable(err) {
				logger.Info("ServiceMonitor CRD not available, skipping ServiceMonitor deletion")
			} else {
				logger.Error(err, "Failed to delete ServiceMonitor")
				return r.updateStatus(ctx, instance, "Failed", err.Error())
			}
		}
	}

	if instance.Spec.Ingress != nil && instance.Spec.Ingress.Enabled != nil && *instance.Spec.Ingress.Enabled {
		if err := r.reconcileIngress(ctx, instance); err != nil {
			logger.Error(err, "Failed to reconcile Ingress")
			r.Recorder.Event(instance, corev1.EventTypeWarning, "IngressFailed", err.Error())
			return r.updateStatus(ctx, instance, "Failed", err.Error())
		}
	} else {
		if err := r.deleteIngressIfExists(ctx, instance); err != nil {
			logger.Error(err, "Failed to delete Ingress")
		}
	}

	return r.updateStatusFromDeployments(ctx, instance)
}

// handleDeletion handles cleanup when the resource is being deleted
func (r *N8nInstanceReconciler) handleDeletion(ctx context.Context, instance *n8nv1alpha1.N8nInstance) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(instance, n8nFinalizer) {
		logger.Info("Performing cleanup for N8nInstance", "name", instance.Name)
		r.Recorder.Event(instance, corev1.EventTypeNormal, "Deleting", "Cleaning up resources")

		controllerutil.RemoveFinalizer(instance, n8nFinalizer)
		if err := r.Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// deleteIngressIfExists deletes the Ingress if it exists
func (r *N8nInstanceReconciler) deleteIngressIfExists(ctx context.Context, instance *n8nv1alpha1.N8nInstance) error {
	ingress := &networkingv1.Ingress{}
	err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, ingress)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return r.Delete(ctx, ingress)
}

func (r *N8nInstanceReconciler) setDefaults(instance *n8nv1alpha1.N8nInstance) {
	if instance.Spec.Replicas == nil {
		replicas := int32(1)
		instance.Spec.Replicas = &replicas
	}
	if instance.Spec.Image == "" {
		instance.Spec.Image = "docker.n8n.io/n8nio/n8n:latest"
	}
	if instance.Spec.ImagePullPolicy == "" {
		instance.Spec.ImagePullPolicy = corev1.PullIfNotPresent
	}
	if instance.Spec.Timezone == "" {
		instance.Spec.Timezone = "UTC"
	}
	if instance.Spec.GenericTimezone == nil {
		instance.Spec.GenericTimezone = boolPtr(true)
	}
}

func (r *N8nInstanceReconciler) queueEnabled(instance *n8nv1alpha1.N8nInstance) bool {
	return instance.Spec.Queue != nil && instance.Spec.Queue.Enabled != nil && *instance.Spec.Queue.Enabled
}

func (r *N8nInstanceReconciler) queueWorkerEnabled(instance *n8nv1alpha1.N8nInstance) bool {
	if !r.queueEnabled(instance) {
		return false
	}
	if instance.Spec.Queue.Worker == nil || instance.Spec.Queue.Worker.Enabled == nil {
		return true
	}
	return *instance.Spec.Queue.Worker.Enabled
}

func (r *N8nInstanceReconciler) desiredMainReplicas(instance *n8nv1alpha1.N8nInstance) int32 {
	if r.queueEnabled(instance) {
		return 1
	}
	if instance.Spec.Replicas != nil {
		return *instance.Spec.Replicas
	}
	return 1
}

func (r *N8nInstanceReconciler) desiredWorkerReplicas(instance *n8nv1alpha1.N8nInstance) int32 {
	if instance.Spec.Queue != nil && instance.Spec.Queue.Worker != nil && instance.Spec.Queue.Worker.Replicas != nil {
		return *instance.Spec.Queue.Worker.Replicas
	}
	if instance.Spec.Replicas == nil {
		return 1
	}
	if *instance.Spec.Replicas <= 1 {
		return 1
	}
	return *instance.Spec.Replicas - 1
}

func workerDeploymentName(instance *n8nv1alpha1.N8nInstance) string {
	return fmt.Sprintf("%s-worker", instance.Name)
}

func (r *N8nInstanceReconciler) reconcilePVC(ctx context.Context, instance *n8nv1alpha1.N8nInstance) error {
	logger := log.FromContext(ctx)
	persistence := instance.Spec.Persistence

	if persistence.ExistingClaim != "" {
		return nil
	}

	pvcName := fmt.Sprintf("%s-data", instance.Name)
	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: instance.Namespace}, pvc)

	if errors.IsNotFound(err) {
		pvc = r.buildPVC(instance, pvcName)
		if err := controllerutil.SetControllerReference(instance, pvc, r.Scheme); err != nil {
			return err
		}
		logger.Info("Creating PVC", "name", pvcName)
		r.Recorder.Event(instance, corev1.EventTypeNormal, "PVCCreated", fmt.Sprintf("Created PVC %s", pvcName))
		return r.Create(ctx, pvc)
	}

	return err
}

func (r *N8nInstanceReconciler) buildPVC(instance *n8nv1alpha1.N8nInstance, name string) *corev1.PersistentVolumeClaim {
	persistence := instance.Spec.Persistence
	size := "1Gi"
	if persistence.Size != "" {
		size = persistence.Size
	}

	accessModes := persistence.AccessModes
	if len(accessModes) == 0 {
		accessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: instance.Namespace,
			Labels:    r.buildLabels(instance, n8nComponentMain),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: accessModes,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(size),
				},
			},
		},
	}

	if persistence.StorageClass != nil {
		pvc.Spec.StorageClassName = persistence.StorageClass
	}

	return pvc
}

func (r *N8nInstanceReconciler) reconcileService(ctx context.Context, instance *n8nv1alpha1.N8nInstance) error {
	logger := log.FromContext(ctx)

	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, svc)

	desiredSvc := r.buildService(instance)
	if err := controllerutil.SetControllerReference(instance, desiredSvc, r.Scheme); err != nil {
		return err
	}

	if errors.IsNotFound(err) {
		logger.Info("Creating Service", "name", instance.Name)
		r.Recorder.Event(instance, corev1.EventTypeNormal, "ServiceCreated", fmt.Sprintf("Created Service %s", instance.Name))
		return r.Create(ctx, desiredSvc)
	}
	if err != nil {
		return err
	}

	needsUpdate := !apiequality.Semantic.DeepEqual(svc.Spec.Ports, desiredSvc.Spec.Ports) ||
		svc.Spec.Type != desiredSvc.Spec.Type ||
		!apiequality.Semantic.DeepEqual(svc.Spec.Selector, desiredSvc.Spec.Selector) ||
		!apiequality.Semantic.DeepEqual(svc.Labels, desiredSvc.Labels) ||
		!apiequality.Semantic.DeepEqual(svc.Annotations, desiredSvc.Annotations)

	if needsUpdate {
		svc.Spec.Ports = desiredSvc.Spec.Ports
		svc.Spec.Type = desiredSvc.Spec.Type
		svc.Spec.Selector = desiredSvc.Spec.Selector
		svc.Labels = desiredSvc.Labels
		svc.Annotations = desiredSvc.Annotations
		logger.Info("Updating Service", "name", instance.Name)
		return r.Update(ctx, svc)
	}

	return nil
}

func (r *N8nInstanceReconciler) buildService(instance *n8nv1alpha1.N8nInstance) *corev1.Service {
	port := int32(5678)
	svcType := corev1.ServiceTypeClusterIP

	if instance.Spec.Service != nil {
		if instance.Spec.Service.Port != 0 {
			port = instance.Spec.Service.Port
		}
		if instance.Spec.Service.Type != "" {
			svcType = instance.Spec.Service.Type
		}
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        instance.Name,
			Namespace:   instance.Namespace,
			Labels:      r.buildLabels(instance, n8nComponentMain),
			Annotations: map[string]string{},
		},
		Spec: corev1.ServiceSpec{
			Type:     svcType,
			Selector: r.buildSelectorLabels(instance, n8nComponentMain),
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       port,
					TargetPort: intstr.FromInt32(5678),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	if instance.Spec.Service != nil {
		if instance.Spec.Service.Annotations != nil {
			svc.Annotations = instance.Spec.Service.Annotations
		}
		if instance.Spec.Service.Labels != nil {
			for k, v := range instance.Spec.Service.Labels {
				svc.Labels[k] = v
			}
		}
		if instance.Spec.Service.NodePort != nil && (svcType == corev1.ServiceTypeNodePort || svcType == corev1.ServiceTypeLoadBalancer) {
			svc.Spec.Ports[0].NodePort = *instance.Spec.Service.NodePort
		}
	}

	if instance.Spec.Metrics != nil && instance.Spec.Metrics.Enabled != nil && *instance.Spec.Metrics.Enabled {
		metricsPort := int32(5679)
		if instance.Spec.Metrics.Port != nil {
			metricsPort = *instance.Spec.Metrics.Port
		}
		svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
			Name:       "metrics",
			Port:       metricsPort,
			TargetPort: intstr.FromInt32(metricsPort),
			Protocol:   corev1.ProtocolTCP,
		})
	}

	return svc
}

func (r *N8nInstanceReconciler) reconcileDeployment(ctx context.Context, instance *n8nv1alpha1.N8nInstance, component, name string, replicas int32) (bool, error) {
	logger := log.FromContext(ctx)

	deploy := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: instance.Namespace}, deploy)

	desiredDeploy, buildErr := r.buildDeployment(ctx, instance, component, name, replicas)
	if buildErr != nil {
		return false, buildErr
	}
	if err := controllerutil.SetControllerReference(instance, desiredDeploy, r.Scheme); err != nil {
		return false, err
	}

	if errors.IsNotFound(err) {
		logger.Info("Creating Deployment", "name", name, "component", component)
		r.Recorder.Event(instance, corev1.EventTypeNormal, "DeploymentCreated", fmt.Sprintf("Created %s Deployment %s", component, name))
		return true, r.Create(ctx, desiredDeploy)
	}
	if err != nil {
		return false, err
	}

	needsUpdate := !apiequality.Semantic.DeepEqual(deploy.Labels, desiredDeploy.Labels) ||
		!apiequality.Semantic.DeepEqual(deploy.Annotations, desiredDeploy.Annotations) ||
		!apiequality.Semantic.DeepEqual(deploy.Spec, desiredDeploy.Spec)

	if needsUpdate {
		deploy.Labels = desiredDeploy.Labels
		deploy.Annotations = desiredDeploy.Annotations
		deploy.Spec = desiredDeploy.Spec
		logger.Info("Updating Deployment", "name", name, "component", component)
		return true, r.Update(ctx, deploy)
	}

	return false, nil
}

func (r *N8nInstanceReconciler) deleteDeploymentIfExists(ctx context.Context, namespace, name string) error {
	deploy := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deploy)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return r.Delete(ctx, deploy)
}

func (r *N8nInstanceReconciler) buildDeployment(ctx context.Context, instance *n8nv1alpha1.N8nInstance, component, name string, replicas int32) (*appsv1.Deployment, error) {
	labels := r.buildLabels(instance, component)
	selectorLabels := r.buildSelectorLabels(instance, component)

	env, err := r.buildEnvVars(ctx, instance, component)
	if err != nil {
		return nil, err
	}

	volumes, volumeMounts := r.buildVolumes(instance, component)

	container := corev1.Container{
		Name:            "n8n",
		Image:           instance.Spec.Image,
		ImagePullPolicy: instance.Spec.ImagePullPolicy,
		Env:             env,
		EnvFrom:         instance.Spec.ExtraEnvFrom,
		VolumeMounts:    volumeMounts,
		Resources:       instance.Spec.Resources,
	}

	if component == n8nComponentMain {
		container.Ports = []corev1.ContainerPort{{Name: "http", ContainerPort: 5678, Protocol: corev1.ProtocolTCP}}
		if instance.Spec.Metrics != nil && instance.Spec.Metrics.Enabled != nil && *instance.Spec.Metrics.Enabled {
			metricsPort := int32(5679)
			if instance.Spec.Metrics.Port != nil {
				metricsPort = *instance.Spec.Metrics.Port
			}
			container.Ports = append(container.Ports, corev1.ContainerPort{Name: "metrics", ContainerPort: metricsPort, Protocol: corev1.ProtocolTCP})
		}
		r.configureProbes(&container, instance)
	} else {
		container.Command = []string{"n8n"}
		container.Args = []string{"worker"}
	}

	if instance.Spec.SecurityContext != nil {
		container.SecurityContext = instance.Spec.SecurityContext
	}

	containers := []corev1.Container{container}
	if component == n8nComponentMain {
		containers = append(containers, instance.Spec.SidecarContainers...)
	}

	podAnnotations := map[string]string{}
	if instance.Spec.PodAnnotations != nil {
		podAnnotations = instance.Spec.PodAnnotations
	}

	podLabels := make(map[string]string)
	for k, v := range selectorLabels {
		podLabels[k] = v
	}
	if instance.Spec.PodLabels != nil {
		for k, v := range instance.Spec.PodLabels {
			podLabels[k] = v
		}
	}

	replicaCount := replicas
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicaCount,
			Selector: &metav1.LabelSelector{MatchLabels: selectorLabels},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 0},
					MaxSurge:       &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels, Annotations: podAnnotations},
				Spec: corev1.PodSpec{
					ServiceAccountName: instance.Spec.ServiceAccountName,
					InitContainers:     instance.Spec.InitContainers,
					Containers:         containers,
					Volumes:            volumes,
					NodeSelector:       instance.Spec.NodeSelector,
					Tolerations:        instance.Spec.Tolerations,
					Affinity:           instance.Spec.Affinity,
					ImagePullSecrets:   instance.Spec.ImagePullSecrets,
				},
			},
		},
	}

	if instance.Spec.PodSecurityContext != nil {
		deploy.Spec.Template.Spec.SecurityContext = instance.Spec.PodSecurityContext
	}

	return deploy, nil
}

func (r *N8nInstanceReconciler) buildEnvVars(ctx context.Context, instance *n8nv1alpha1.N8nInstance, component string) ([]corev1.EnvVar, error) {
	_ = ctx
	env := []corev1.EnvVar{
		{Name: "N8N_PORT", Value: "5678"},
		{Name: "N8N_PROTOCOL", Value: "http"},
	}

	if instance.Spec.GenericTimezone == nil || *instance.Spec.GenericTimezone {
		env = append(env, corev1.EnvVar{Name: "GENERIC_TIMEZONE", Value: instance.Spec.Timezone})
	} else {
		env = append(env, corev1.EnvVar{Name: "TZ", Value: instance.Spec.Timezone})
	}

	db := instance.Spec.Database
	env = append(env, corev1.EnvVar{Name: "DB_TYPE", Value: db.Type})

	dbPrefix := dbEnvPrefix(db.Type)
	if db.SecretRef != nil {
		if db.Type == "sqlite" {
			env = append(env, secretEnvVar(dbPrefix+"DATABASE", db.SecretRef.Name, "database", true))
		} else {
			env = append(env,
				secretEnvVar(dbPrefix+"HOST", db.SecretRef.Name, "host", true),
				secretEnvVar(dbPrefix+"PORT", db.SecretRef.Name, "port", true),
				secretEnvVar(dbPrefix+"DATABASE", db.SecretRef.Name, "database", true),
				secretEnvVar(dbPrefix+"USER", db.SecretRef.Name, "user", true),
				secretEnvVar(dbPrefix+"PASSWORD", db.SecretRef.Name, "password", true),
			)
		}
	}

	if db.SSL != nil && *db.SSL && db.Type != "sqlite" {
		env = append(env, corev1.EnvVar{Name: dbPrefix + "SSL_ENABLED", Value: "true"})
	}
	if db.SSLRejectUnauthorized != nil && !*db.SSLRejectUnauthorized && db.Type != "sqlite" {
		env = append(env, corev1.EnvVar{Name: dbPrefix + "SSL_REJECT_UNAUTHORIZED", Value: "false"})
	}
	if db.TablePrefix != "" {
		env = append(env, corev1.EnvVar{Name: "DB_TABLE_PREFIX", Value: db.TablePrefix})
	}
	if db.Logging != "" {
		// Map "none" to "false" for n8n compatibility
		loggingValue := db.Logging
		if loggingValue == "none" {
			loggingValue = "false"
		}
		env = append(env, corev1.EnvVar{Name: "DB_LOGGING_ENABLED", Value: loggingValue})
	}

	if r.queueEnabled(instance) {
		env = append(env, corev1.EnvVar{Name: "EXECUTIONS_MODE", Value: "queue"})

		if instance.Spec.Queue.Redis != nil {
			if instance.Spec.Queue.Redis.SecretRef != nil {
				redisSecret := instance.Spec.Queue.Redis.SecretRef.Name
				env = append(env,
					secretEnvVar("QUEUE_BULL_REDIS_HOST", redisSecret, "host", true),
					secretEnvVar("QUEUE_BULL_REDIS_PORT", redisSecret, "port", true),
					secretEnvVar("QUEUE_BULL_REDIS_PASSWORD", redisSecret, "password", true),
					secretEnvVar("QUEUE_BULL_REDIS_DB", redisSecret, "db", true),
				)
			}
			if instance.Spec.Queue.Redis.DB != nil {
				env = append(env, corev1.EnvVar{Name: "QUEUE_BULL_REDIS_DB", Value: fmt.Sprintf("%d", *instance.Spec.Queue.Redis.DB)})
			}
			if instance.Spec.Queue.Redis.ClusterNodes != "" {
				env = append(env, corev1.EnvVar{Name: "QUEUE_BULL_REDIS_CLUSTER_NODES", Value: instance.Spec.Queue.Redis.ClusterNodes})
			}
			if instance.Spec.Queue.Redis.SSL != nil && *instance.Spec.Queue.Redis.SSL {
				env = append(env, corev1.EnvVar{Name: "QUEUE_BULL_REDIS_TLS", Value: "true"})
			}
		}

		if instance.Spec.Queue.BullMQ != nil {
			if instance.Spec.Queue.BullMQ.Prefix != "" {
				env = append(env, corev1.EnvVar{Name: "QUEUE_BULL_PREFIX", Value: instance.Spec.Queue.BullMQ.Prefix})
			}
			if instance.Spec.Queue.BullMQ.GracefulShutdownTimeout != nil {
				env = append(env, corev1.EnvVar{Name: "QUEUE_BULL_GRACEFUL_SHUTDOWN_TIMEOUT", Value: fmt.Sprintf("%d", *instance.Spec.Queue.BullMQ.GracefulShutdownTimeout)})
			}
		}

		if instance.Spec.Queue.Health != nil && instance.Spec.Queue.Health.Active != nil && *instance.Spec.Queue.Health.Active {
			env = append(env, corev1.EnvVar{Name: "QUEUE_HEALTH_CHECK_ACTIVE", Value: "true"})
		}
	}

	if instance.Spec.Encryption != nil && instance.Spec.Encryption.KeySecretRef != nil {
		keyRef := instance.Spec.Encryption.KeySecretRef
		env = append(env, corev1.EnvVar{Name: "N8N_ENCRYPTION_KEY", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: keyRef.Name}, Key: keyRef.Key},
		}})
	}

	if instance.Spec.Webhook != nil {
		if instance.Spec.Webhook.URL != "" {
			env = append(env, corev1.EnvVar{Name: "WEBHOOK_URL", Value: instance.Spec.Webhook.URL})
		}
		if instance.Spec.Webhook.TunnelEnabled != nil && *instance.Spec.Webhook.TunnelEnabled {
			env = append(env, corev1.EnvVar{Name: "N8N_TUNNEL_ENABLED", Value: "true"})
		}
		if instance.Spec.Webhook.Path != "" {
			env = append(env, corev1.EnvVar{Name: "N8N_PATH", Value: instance.Spec.Webhook.Path})
		}
	}

	if instance.Spec.SMTP != nil && instance.Spec.SMTP.Enabled != nil && *instance.Spec.SMTP.Enabled {
		env = append(env, corev1.EnvVar{Name: "N8N_EMAIL_MODE", Value: "smtp"})
		if instance.Spec.SMTP.SecretRef != nil {
			smtpSecret := instance.Spec.SMTP.SecretRef.Name
			env = append(env,
				secretEnvVar("N8N_SMTP_HOST", smtpSecret, "host", true),
				secretEnvVar("N8N_SMTP_PORT", smtpSecret, "port", true),
				secretEnvVar("N8N_SMTP_USER", smtpSecret, "user", true),
				secretEnvVar("N8N_SMTP_PASS", smtpSecret, "password", true),
				secretEnvVar("N8N_SMTP_SENDER", smtpSecret, "sender", true),
			)
		}
		if instance.Spec.SMTP.SSL != nil && *instance.Spec.SMTP.SSL {
			env = append(env, corev1.EnvVar{Name: "N8N_SMTP_SSL", Value: "true"})
		}
	}

	if instance.Spec.Executions != nil {
		exec := instance.Spec.Executions
		if exec.Mode != "" {
			env = append(env, corev1.EnvVar{Name: "EXECUTIONS_MODE", Value: exec.Mode})
		}
		if exec.Timeout != nil {
			env = append(env, corev1.EnvVar{Name: "EXECUTIONS_TIMEOUT", Value: fmt.Sprintf("%d", *exec.Timeout)})
		}
		if exec.MaxTimeout != nil {
			env = append(env, corev1.EnvVar{Name: "EXECUTIONS_TIMEOUT_MAX", Value: fmt.Sprintf("%d", *exec.MaxTimeout)})
		}
		if exec.SaveDataOnError != "" {
			env = append(env, corev1.EnvVar{Name: "EXECUTIONS_DATA_SAVE_ON_ERROR", Value: exec.SaveDataOnError})
		}
		if exec.SaveDataOnSuccess != "" {
			env = append(env, corev1.EnvVar{Name: "EXECUTIONS_DATA_SAVE_ON_SUCCESS", Value: exec.SaveDataOnSuccess})
		}
		if exec.SaveManualExecutions != nil {
			env = append(env, corev1.EnvVar{Name: "EXECUTIONS_DATA_SAVE_MANUAL_EXECUTIONS", Value: fmt.Sprintf("%t", *exec.SaveManualExecutions)})
		}
		if exec.PruneData != nil && *exec.PruneData {
			env = append(env, corev1.EnvVar{Name: "EXECUTIONS_DATA_PRUNE", Value: "true"})
			if exec.PruneDataMaxAge != "" {
				env = append(env, corev1.EnvVar{Name: "EXECUTIONS_DATA_MAX_AGE", Value: exec.PruneDataMaxAge})
			}
			if exec.PruneDataMaxCount != nil {
				env = append(env, corev1.EnvVar{Name: "EXECUTIONS_DATA_MAX_COUNT", Value: fmt.Sprintf("%d", *exec.PruneDataMaxCount)})
			}
		}
	}

	if instance.Spec.Logging != nil {
		if instance.Spec.Logging.Level != "" {
			env = append(env, corev1.EnvVar{Name: "N8N_LOG_LEVEL", Value: instance.Spec.Logging.Level})
		}
		if instance.Spec.Logging.Output != "" {
			env = append(env, corev1.EnvVar{Name: "N8N_LOG_OUTPUT", Value: instance.Spec.Logging.Output})
		}
	}

	if instance.Spec.Metrics != nil && instance.Spec.Metrics.Enabled != nil && *instance.Spec.Metrics.Enabled {
		env = append(env, corev1.EnvVar{Name: "N8N_METRICS", Value: "true"})
		if instance.Spec.Metrics.Port != nil {
			env = append(env, corev1.EnvVar{Name: "N8N_METRICS_PORT", Value: fmt.Sprintf("%d", *instance.Spec.Metrics.Port)})
		}
		if instance.Spec.Metrics.IncludeWorkflowIdLabel != nil && *instance.Spec.Metrics.IncludeWorkflowIdLabel {
			env = append(env, corev1.EnvVar{Name: "N8N_METRICS_INCLUDE_WORKFLOW_ID_LABEL", Value: "true"})
		}
		if instance.Spec.Metrics.IncludeNodeTypeLabel != nil && *instance.Spec.Metrics.IncludeNodeTypeLabel {
			env = append(env, corev1.EnvVar{Name: "N8N_METRICS_INCLUDE_NODE_TYPE_LABEL", Value: "true"})
		}
		if instance.Spec.Metrics.IncludeCredentialTypeLabel != nil && *instance.Spec.Metrics.IncludeCredentialTypeLabel {
			env = append(env, corev1.EnvVar{Name: "N8N_METRICS_INCLUDE_CREDENTIAL_TYPE_LABEL", Value: "true"})
		}
		if instance.Spec.Metrics.IncludeApiEndpoints != nil && *instance.Spec.Metrics.IncludeApiEndpoints {
			env = append(env, corev1.EnvVar{Name: "N8N_METRICS_INCLUDE_API_ENDPOINTS", Value: "true"})
		}
		if instance.Spec.Metrics.IncludeMessageEventBusMetrics != nil && *instance.Spec.Metrics.IncludeMessageEventBusMetrics {
			env = append(env, corev1.EnvVar{Name: "N8N_METRICS_INCLUDE_MESSAGE_EVENT_BUS_METRICS", Value: "true"})
		}
	}

	if instance.Spec.ExternalHooks != nil && instance.Spec.ExternalHooks.Files != "" {
		env = append(env, corev1.EnvVar{Name: "EXTERNAL_HOOK_FILES", Value: instance.Spec.ExternalHooks.Files})
	}

	if component == n8nComponentWorker {
		env = append(env, corev1.EnvVar{Name: "N8N_RUNNERS_ENABLED", Value: "true"})
	}

	env = append(env, instance.Spec.ExtraEnv...)

	return env, nil
}

func dbEnvPrefix(dbType string) string {
	switch dbType {
	case "mysqldb", "mariadb":
		return "DB_MYSQLDB_"
	case "sqlite":
		return "DB_SQLITE_"
	default:
		return "DB_POSTGRESDB_"
	}
}

func secretEnvVar(envName, secretName, key string, optional bool) corev1.EnvVar {
	return corev1.EnvVar{Name: envName, ValueFrom: &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
			Key:                  key,
			Optional:             boolPtr(optional),
		},
	}}
}

func (r *N8nInstanceReconciler) buildVolumes(instance *n8nv1alpha1.N8nInstance, component string) ([]corev1.Volume, []corev1.VolumeMount) {
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount

	if component == n8nComponentMain &&
		instance.Spec.Persistence != nil &&
		(instance.Spec.Persistence.Enabled == nil || *instance.Spec.Persistence.Enabled) {
		pvcName := fmt.Sprintf("%s-data", instance.Name)
		if instance.Spec.Persistence.ExistingClaim != "" {
			pvcName = instance.Spec.Persistence.ExistingClaim
		}

		volumes = append(volumes, corev1.Volume{
			Name: "data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: "data", MountPath: "/home/node/.n8n"})
	}

	volumes = append(volumes, instance.Spec.ExtraVolumes...)
	mounts = append(mounts, instance.Spec.ExtraVolumeMounts...)

	return volumes, mounts
}

func (r *N8nInstanceReconciler) configureProbes(container *corev1.Container, instance *n8nv1alpha1.N8nInstance) {
	container.LivenessProbe = &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(5678)}},
		InitialDelaySeconds: 30,
		PeriodSeconds:       10,
		TimeoutSeconds:      5,
		FailureThreshold:    6,
	}

	container.ReadinessProbe = &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(5678)}},
		InitialDelaySeconds: 5,
		PeriodSeconds:       5,
		TimeoutSeconds:      5,
		FailureThreshold:    3,
	}

	container.StartupProbe = &corev1.Probe{
		ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt32(5678)}},
		InitialDelaySeconds: 10,
		PeriodSeconds:       10,
		TimeoutSeconds:      5,
		FailureThreshold:    30,
	}

	if instance.Spec.HealthCheck != nil {
		if instance.Spec.HealthCheck.LivenessProbe != nil {
			if instance.Spec.HealthCheck.LivenessProbe.Enabled != nil && !*instance.Spec.HealthCheck.LivenessProbe.Enabled {
				container.LivenessProbe = nil
			} else {
				r.applyProbeConfig(container.LivenessProbe, instance.Spec.HealthCheck.LivenessProbe)
			}
		}
		if instance.Spec.HealthCheck.ReadinessProbe != nil {
			if instance.Spec.HealthCheck.ReadinessProbe.Enabled != nil && !*instance.Spec.HealthCheck.ReadinessProbe.Enabled {
				container.ReadinessProbe = nil
			} else {
				r.applyProbeConfig(container.ReadinessProbe, instance.Spec.HealthCheck.ReadinessProbe)
			}
		}
		if instance.Spec.HealthCheck.StartupProbe != nil {
			if instance.Spec.HealthCheck.StartupProbe.Enabled != nil && !*instance.Spec.HealthCheck.StartupProbe.Enabled {
				container.StartupProbe = nil
			} else {
				r.applyProbeConfig(container.StartupProbe, instance.Spec.HealthCheck.StartupProbe)
			}
		}
	}
}

func (r *N8nInstanceReconciler) applyProbeConfig(probe *corev1.Probe, config *n8nv1alpha1.ProbeConfig) {
	if probe == nil {
		return
	}
	if config.InitialDelaySeconds != nil {
		probe.InitialDelaySeconds = *config.InitialDelaySeconds
	}
	if config.PeriodSeconds != nil {
		probe.PeriodSeconds = *config.PeriodSeconds
	}
	if config.TimeoutSeconds != nil {
		probe.TimeoutSeconds = *config.TimeoutSeconds
	}
	if config.FailureThreshold != nil {
		probe.FailureThreshold = *config.FailureThreshold
	}
	if config.SuccessThreshold != nil {
		probe.SuccessThreshold = *config.SuccessThreshold
	}
}

func (r *N8nInstanceReconciler) reconcileIngress(ctx context.Context, instance *n8nv1alpha1.N8nInstance) error {
	logger := log.FromContext(ctx)

	ingress := &networkingv1.Ingress{}
	err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, ingress)

	desiredIngress := r.buildIngress(instance)
	if err := controllerutil.SetControllerReference(instance, desiredIngress, r.Scheme); err != nil {
		return err
	}

	if errors.IsNotFound(err) {
		logger.Info("Creating Ingress", "name", instance.Name)
		r.Recorder.Event(instance, corev1.EventTypeNormal, "IngressCreated", fmt.Sprintf("Created Ingress %s", instance.Name))
		return r.Create(ctx, desiredIngress)
	}
	if err != nil {
		return err
	}

	if !apiequality.Semantic.DeepEqual(ingress.Spec, desiredIngress.Spec) ||
		!apiequality.Semantic.DeepEqual(ingress.Annotations, desiredIngress.Annotations) ||
		!apiequality.Semantic.DeepEqual(ingress.Labels, desiredIngress.Labels) {
		ingress.Spec = desiredIngress.Spec
		ingress.Annotations = desiredIngress.Annotations
		ingress.Labels = desiredIngress.Labels
		logger.Info("Updating Ingress", "name", instance.Name)
		return r.Update(ctx, ingress)
	}

	return nil
}

func (r *N8nInstanceReconciler) buildIngress(instance *n8nv1alpha1.N8nInstance) *networkingv1.Ingress {
	ingressSpec := instance.Spec.Ingress
	pathType := networkingv1.PathTypePrefix
	if ingressSpec.PathType == "Exact" {
		pathType = networkingv1.PathTypeExact
	}

	path := "/"
	if ingressSpec.Path != "" {
		path = ingressSpec.Path
	}

	port := int32(5678)
	if instance.Spec.Service != nil && instance.Spec.Service.Port != 0 {
		port = instance.Spec.Service.Port
	}

	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        instance.Name,
			Namespace:   instance.Namespace,
			Labels:      r.buildLabels(instance, n8nComponentMain),
			Annotations: ingressSpec.Annotations,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ingressSpec.ClassName,
			Rules: []networkingv1.IngressRule{{
				Host: ingressSpec.Host,
				IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
					Path:     path,
					PathType: &pathType,
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
						Name: instance.Name,
						Port: networkingv1.ServiceBackendPort{Number: port},
					}},
				}}}},
			}},
		},
	}

	if len(ingressSpec.TLS) > 0 {
		for _, tls := range ingressSpec.TLS {
			ingress.Spec.TLS = append(ingress.Spec.TLS, networkingv1.IngressTLS{Hosts: tls.Hosts, SecretName: tls.SecretName})
		}
	}

	return ingress
}

func (r *N8nInstanceReconciler) serviceMonitorEnabled(instance *n8nv1alpha1.N8nInstance) bool {
	if instance.Spec.Metrics == nil || instance.Spec.Metrics.Enabled == nil || !*instance.Spec.Metrics.Enabled {
		return false
	}
	if instance.Spec.Metrics.ServiceMonitor == nil || instance.Spec.Metrics.ServiceMonitor.Enabled == nil {
		return false
	}
	return *instance.Spec.Metrics.ServiceMonitor.Enabled
}

func (r *N8nInstanceReconciler) reconcileServiceMonitor(ctx context.Context, instance *n8nv1alpha1.N8nInstance) error {
	logger := log.FromContext(ctx)

	sm := &promv1.ServiceMonitor{}
	err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, sm)

	desired := r.buildServiceMonitor(instance)
	if err := controllerutil.SetControllerReference(instance, desired, r.Scheme); err != nil {
		return err
	}

	if errors.IsNotFound(err) {
		logger.Info("Creating ServiceMonitor", "name", desired.Name)
		r.Recorder.Event(instance, corev1.EventTypeNormal, "ServiceMonitorCreated", fmt.Sprintf("Created ServiceMonitor %s", desired.Name))
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if !apiequality.Semantic.DeepEqual(sm.Labels, desired.Labels) ||
		!apiequality.Semantic.DeepEqual(sm.Annotations, desired.Annotations) ||
		!apiequality.Semantic.DeepEqual(sm.Spec, desired.Spec) {
		sm.Labels = desired.Labels
		sm.Annotations = desired.Annotations
		sm.Spec = desired.Spec
		logger.Info("Updating ServiceMonitor", "name", desired.Name)
		return r.Update(ctx, sm)
	}

	return nil
}

func (r *N8nInstanceReconciler) buildServiceMonitor(instance *n8nv1alpha1.N8nInstance) *promv1.ServiceMonitor {
	interval := promv1.Duration("30s")
	if instance.Spec.Metrics != nil && instance.Spec.Metrics.ServiceMonitor != nil && instance.Spec.Metrics.ServiceMonitor.Interval != "" {
		interval = promv1.Duration(instance.Spec.Metrics.ServiceMonitor.Interval)
	}

	labels := r.buildLabels(instance, n8nComponentMain)
	if instance.Spec.Metrics != nil && instance.Spec.Metrics.ServiceMonitor != nil && instance.Spec.Metrics.ServiceMonitor.Labels != nil {
		for k, v := range instance.Spec.Metrics.ServiceMonitor.Labels {
			labels[k] = v
		}
	}

	return &promv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name,
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: promv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{MatchLabels: r.buildSelectorLabels(instance, n8nComponentMain)},
			NamespaceSelector: promv1.NamespaceSelector{
				MatchNames: []string{instance.Namespace},
			},
			Endpoints: []promv1.Endpoint{{
				Port:     "metrics",
				Path:     "/metrics",
				Interval: interval,
			}},
		},
	}
}

func (r *N8nInstanceReconciler) deleteServiceMonitorIfExists(ctx context.Context, instance *n8nv1alpha1.N8nInstance) error {
	sm := &promv1.ServiceMonitor{}
	err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, sm)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return r.Delete(ctx, sm)
}

func isServiceMonitorUnavailable(err error) bool {
	return apimeta.IsNoMatchError(err)
}

func (r *N8nInstanceReconciler) buildLabels(instance *n8nv1alpha1.N8nInstance, component string) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":       "n8n",
		"app.kubernetes.io/instance":   instance.Name,
		"app.kubernetes.io/version":    extractVersion(instance.Spec.Image),
		"app.kubernetes.io/managed-by": "n8n-operator",
	}
	if component != "" {
		labels["n8n.n8n.io/component"] = component
	}
	return labels
}

func (r *N8nInstanceReconciler) buildSelectorLabels(instance *n8nv1alpha1.N8nInstance, component string) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":     "n8n",
		"app.kubernetes.io/instance": instance.Name,
	}
	if component != "" {
		labels["n8n.n8n.io/component"] = component
	}
	return labels
}

// extractVersion extracts the version tag from an image reference
// Handles formats like: image:tag, image:tag@sha256:..., image@sha256:...
func extractVersion(image string) string {
	// Strip digest if present
	if atIdx := strings.Index(image, "@"); atIdx != -1 {
		image = image[:atIdx]
	}

	// Find the version tag after the last colon
	for i := len(image) - 1; i >= 0; i-- {
		if image[i] == ':' {
			version := image[i+1:]
			// Ensure label value doesn't exceed 63 chars
			if len(version) > 63 {
				return version[:63]
			}
			return version
		}
		if image[i] == '/' {
			break
		}
	}
	return "latest"
}

func (r *N8nInstanceReconciler) updateStatus(ctx context.Context, instance *n8nv1alpha1.N8nInstance, phase, message string) (ctrl.Result, error) {
	return r.updateStatusFields(
		ctx,
		instance,
		phase,
		message,
		instance.Status.Replicas,
		instance.Status.ReadyReplicas,
		instance.Status.WorkerReplicas,
		instance.Status.ReadyWorkerReplicas,
		instance.Status.URL,
	)
}

func (r *N8nInstanceReconciler) updateStatusFields(
	ctx context.Context,
	instance *n8nv1alpha1.N8nInstance,
	phase, message string,
	replicas, readyReplicas, workerReplicas, readyWorkerReplicas int32,
	url string,
) (ctrl.Result, error) {
	result := ctrl.Result{RequeueAfter: 5 * time.Minute}
	if phase == "Failed" {
		result = ctrl.Result{RequeueAfter: 30 * time.Second}
	}

	original := instance.DeepCopy()

	instance.Status.Replicas = replicas
	instance.Status.ReadyReplicas = readyReplicas
	instance.Status.WorkerReplicas = workerReplicas
	instance.Status.ReadyWorkerReplicas = readyWorkerReplicas
	instance.Status.URL = url
	instance.Status.Phase = phase
	instance.Status.ObservedGeneration = instance.Generation

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		ObservedGeneration: instance.Generation,
		Reason:             phase,
		Message:            message,
	}
	if phase == "Running" {
		condition.Status = metav1.ConditionTrue
	}
	apimeta.SetStatusCondition(&instance.Status.Conditions, condition)

	if apiequality.Semantic.DeepEqual(original.Status, instance.Status) {
		return result, nil
	}

	if err := r.Status().Update(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}

	return result, nil
}

func (r *N8nInstanceReconciler) updateStatusFromDeployments(ctx context.Context, instance *n8nv1alpha1.N8nInstance) (ctrl.Result, error) {
	mainDeploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, mainDeploy); err != nil {
		return r.updateStatus(ctx, instance, "Pending", "Waiting for main deployment")
	}

	replicas := mainDeploy.Status.Replicas
	readyReplicas := mainDeploy.Status.ReadyReplicas
	workerReplicas := int32(0)
	readyWorkerReplicas := int32(0)

	if r.queueWorkerEnabled(instance) {
		workerDeploy := &appsv1.Deployment{}
		if err := r.Get(ctx, types.NamespacedName{Name: workerDeploymentName(instance), Namespace: instance.Namespace}, workerDeploy); err == nil {
			workerReplicas = workerDeploy.Status.Replicas
			readyWorkerReplicas = workerDeploy.Status.ReadyReplicas
		}
	}

	phase := "Pending"
	message := "Deployment not ready"

	if mainDeploy.Status.ReadyReplicas > 0 && mainDeploy.Status.ReadyReplicas == mainDeploy.Status.Replicas {
		if r.queueWorkerEnabled(instance) && workerReplicas > 0 && readyWorkerReplicas != workerReplicas {
			phase = "Progressing"
			message = "Waiting for worker pods to be ready"
		} else {
			phase = "Running"
			message = "All managed deployments are ready"
		}
	} else if mainDeploy.Status.Replicas > 0 && mainDeploy.Status.ReadyReplicas == 0 {
		phase = "Progressing"
		message = "Waiting for main pods to be ready"
	}

	url := ""
	if instance.Spec.Ingress != nil && instance.Spec.Ingress.Enabled != nil && *instance.Spec.Ingress.Enabled && instance.Spec.Ingress.Host != "" {
		scheme := "http"
		if len(instance.Spec.Ingress.TLS) > 0 {
			scheme = "https"
		}
		url = fmt.Sprintf("%s://%s", scheme, instance.Spec.Ingress.Host)
	} else {
		port := int32(5678)
		if instance.Spec.Service != nil && instance.Spec.Service.Port != 0 {
			port = instance.Spec.Service.Port
		}
		url = fmt.Sprintf("http://%s.%s.svc:%d", instance.Name, instance.Namespace, port)
	}

	return r.updateStatusFields(ctx, instance, phase, message, replicas, readyReplicas, workerReplicas, readyWorkerReplicas, url)
}

func boolPtr(b bool) *bool {
	return &b
}

// SetupWithManager sets up the controller with the Manager.
func (r *N8nInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&n8nv1alpha1.N8nInstance{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&networkingv1.Ingress{}).
		Named("n8ninstance").
		Complete(r)
}
