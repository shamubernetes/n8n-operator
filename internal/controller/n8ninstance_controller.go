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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

// N8nInstanceReconciler reconciles a N8nInstance object
type N8nInstanceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ninstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ninstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ninstances/finalizers,verbs=update
// +kubebuilder:rbac:groups=n8n.n8n.io,resources=n8ninstances/scale,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles the reconciliation of N8nInstance resources
func (r *N8nInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the N8nInstance instance
	instance := &n8nv1alpha1.N8nInstance{}
	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("N8nInstance resource not found, ignoring")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get N8nInstance")
		return ctrl.Result{}, err
	}

	// Set defaults
	r.setDefaults(instance)

	// Reconcile PVC if persistence is enabled
	if instance.Spec.Persistence != nil && (instance.Spec.Persistence.Enabled == nil || *instance.Spec.Persistence.Enabled) {
		if err := r.reconcilePVC(ctx, instance); err != nil {
			logger.Error(err, "Failed to reconcile PVC")
			return r.updateStatus(ctx, instance, "Failed", err.Error())
		}
	}

	// Reconcile Service
	if err := r.reconcileService(ctx, instance); err != nil {
		logger.Error(err, "Failed to reconcile Service")
		return r.updateStatus(ctx, instance, "Failed", err.Error())
	}

	// Reconcile Deployment
	if err := r.reconcileDeployment(ctx, instance); err != nil {
		logger.Error(err, "Failed to reconcile Deployment")
		return r.updateStatus(ctx, instance, "Failed", err.Error())
	}

	// Reconcile Ingress if enabled
	if instance.Spec.Ingress != nil && instance.Spec.Ingress.Enabled != nil && *instance.Spec.Ingress.Enabled {
		if err := r.reconcileIngress(ctx, instance); err != nil {
			logger.Error(err, "Failed to reconcile Ingress")
			return r.updateStatus(ctx, instance, "Failed", err.Error())
		}
	}

	// Update status
	return r.updateStatusFromDeployment(ctx, instance)
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
}

func (r *N8nInstanceReconciler) reconcilePVC(ctx context.Context, instance *n8nv1alpha1.N8nInstance) error {
	logger := log.FromContext(ctx)
	persistence := instance.Spec.Persistence

	// Skip if using existing claim
	if persistence.ExistingClaim != "" {
		return nil
	}

	pvcName := fmt.Sprintf("%s-data", instance.Name)
	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: instance.Namespace}, pvc)

	if errors.IsNotFound(err) {
		// Create PVC
		pvc = r.buildPVC(instance, pvcName)
		if err := controllerutil.SetControllerReference(instance, pvc, r.Scheme); err != nil {
			return err
		}
		logger.Info("Creating PVC", "name", pvcName)
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
			Labels:    r.buildLabels(instance),
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
		return r.Create(ctx, desiredSvc)
	} else if err != nil {
		return err
	}

	// Update Service
	svc.Spec.Ports = desiredSvc.Spec.Ports
	svc.Spec.Type = desiredSvc.Spec.Type
	svc.Spec.Selector = desiredSvc.Spec.Selector
	if instance.Spec.Service != nil && instance.Spec.Service.Annotations != nil {
		svc.Annotations = instance.Spec.Service.Annotations
	}
	return r.Update(ctx, svc)
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
			Labels:      r.buildLabels(instance),
			Annotations: map[string]string{},
		},
		Spec: corev1.ServiceSpec{
			Type:     svcType,
			Selector: r.buildSelectorLabels(instance),
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

	// Add metrics port if enabled
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

func (r *N8nInstanceReconciler) reconcileDeployment(ctx context.Context, instance *n8nv1alpha1.N8nInstance) error {
	logger := log.FromContext(ctx)

	deploy := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, deploy)

	desiredDeploy, err2 := r.buildDeployment(ctx, instance)
	if err2 != nil {
		return err2
	}
	if err3 := controllerutil.SetControllerReference(instance, desiredDeploy, r.Scheme); err3 != nil {
		return err3
	}

	if errors.IsNotFound(err) {
		logger.Info("Creating Deployment", "name", instance.Name)
		return r.Create(ctx, desiredDeploy)
	} else if err != nil {
		return err
	}

	// Update Deployment
	deploy.Spec = desiredDeploy.Spec
	return r.Update(ctx, deploy)
}

func (r *N8nInstanceReconciler) buildDeployment(ctx context.Context, instance *n8nv1alpha1.N8nInstance) (*appsv1.Deployment, error) {
	labels := r.buildLabels(instance)
	selectorLabels := r.buildSelectorLabels(instance)

	// Build environment variables
	env, err := r.buildEnvVars(ctx, instance)
	if err != nil {
		return nil, err
	}

	// Build volumes and mounts
	volumes, volumeMounts := r.buildVolumes(instance)

	container := corev1.Container{
		Name:            "n8n",
		Image:           instance.Spec.Image,
		ImagePullPolicy: instance.Spec.ImagePullPolicy,
		Ports: []corev1.ContainerPort{
			{Name: "http", ContainerPort: 5678, Protocol: corev1.ProtocolTCP},
		},
		Env:          env,
		EnvFrom:      instance.Spec.ExtraEnvFrom,
		VolumeMounts: volumeMounts,
		Resources:    instance.Spec.Resources,
	}

	// Add metrics port
	if instance.Spec.Metrics != nil && instance.Spec.Metrics.Enabled != nil && *instance.Spec.Metrics.Enabled {
		metricsPort := int32(5679)
		if instance.Spec.Metrics.Port != nil {
			metricsPort = *instance.Spec.Metrics.Port
		}
		container.Ports = append(container.Ports, corev1.ContainerPort{
			Name: "metrics", ContainerPort: metricsPort, Protocol: corev1.ProtocolTCP,
		})
	}

	// Configure probes
	r.configureProbes(&container, instance)

	// Apply security context
	if instance.Spec.SecurityContext != nil {
		container.SecurityContext = instance.Spec.SecurityContext
	}

	// Add sidecars
	containers := []corev1.Container{container}
	containers = append(containers, instance.Spec.SidecarContainers...)

	// Build pod annotations
	podAnnotations := map[string]string{}
	if instance.Spec.PodAnnotations != nil {
		podAnnotations = instance.Spec.PodAnnotations
	}

	// Build pod labels
	podLabels := make(map[string]string)
	for k, v := range selectorLabels {
		podLabels[k] = v
	}
	if instance.Spec.PodLabels != nil {
		for k, v := range instance.Spec.PodLabels {
			podLabels[k] = v
		}
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name,
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: instance.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: podAnnotations,
				},
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

func (r *N8nInstanceReconciler) buildEnvVars(ctx context.Context, instance *n8nv1alpha1.N8nInstance) ([]corev1.EnvVar, error) {
	env := []corev1.EnvVar{
		{Name: "N8N_PORT", Value: "5678"},
		{Name: "N8N_PROTOCOL", Value: "http"},
		{Name: "GENERIC_TIMEZONE", Value: instance.Spec.Timezone},
	}

	// Database config
	db := instance.Spec.Database
	env = append(env, corev1.EnvVar{Name: "DB_TYPE", Value: db.Type})

	if db.SecretRef != nil {
		namespace := db.SecretRef.Namespace
		if namespace == "" {
			namespace = instance.Namespace
		}
		env = append(env,
			corev1.EnvVar{Name: "DB_POSTGRESDB_HOST", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: db.SecretRef.Name},
					Key:                  "host",
					Optional:             boolPtr(true),
				},
			}},
			corev1.EnvVar{Name: "DB_POSTGRESDB_PORT", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: db.SecretRef.Name},
					Key:                  "port",
					Optional:             boolPtr(true),
				},
			}},
			corev1.EnvVar{Name: "DB_POSTGRESDB_DATABASE", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: db.SecretRef.Name},
					Key:                  "database",
					Optional:             boolPtr(true),
				},
			}},
			corev1.EnvVar{Name: "DB_POSTGRESDB_USER", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: db.SecretRef.Name},
					Key:                  "user",
					Optional:             boolPtr(true),
				},
			}},
			corev1.EnvVar{Name: "DB_POSTGRESDB_PASSWORD", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: db.SecretRef.Name},
					Key:                  "password",
					Optional:             boolPtr(true),
				},
			}},
		)
	}

	if db.SSL != nil && *db.SSL {
		env = append(env, corev1.EnvVar{Name: "DB_POSTGRESDB_SSL_ENABLED", Value: "true"})
	}
	if db.SSLRejectUnauthorized != nil && !*db.SSLRejectUnauthorized {
		env = append(env, corev1.EnvVar{Name: "DB_POSTGRESDB_SSL_REJECT_UNAUTHORIZED", Value: "false"})
	}
	if db.TablePrefix != "" {
		env = append(env, corev1.EnvVar{Name: "DB_TABLE_PREFIX", Value: db.TablePrefix})
	}

	// Queue mode (Redis)
	if instance.Spec.Queue != nil && instance.Spec.Queue.Enabled != nil && *instance.Spec.Queue.Enabled {
		env = append(env, corev1.EnvVar{Name: "EXECUTIONS_MODE", Value: "queue"})

		if instance.Spec.Queue.Redis != nil && instance.Spec.Queue.Redis.SecretRef != nil {
			redisSecret := instance.Spec.Queue.Redis.SecretRef.Name
			env = append(env,
				corev1.EnvVar{Name: "QUEUE_BULL_REDIS_HOST", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: redisSecret},
						Key:                  "host",
						Optional:             boolPtr(true),
					},
				}},
				corev1.EnvVar{Name: "QUEUE_BULL_REDIS_PORT", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: redisSecret},
						Key:                  "port",
						Optional:             boolPtr(true),
					},
				}},
				corev1.EnvVar{Name: "QUEUE_BULL_REDIS_PASSWORD", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: redisSecret},
						Key:                  "password",
						Optional:             boolPtr(true),
					},
				}},
			)
		}
	}

	// Encryption key
	if instance.Spec.Encryption != nil && instance.Spec.Encryption.KeySecretRef != nil {
		keyRef := instance.Spec.Encryption.KeySecretRef
		env = append(env, corev1.EnvVar{Name: "N8N_ENCRYPTION_KEY", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: keyRef.Name},
				Key:                  keyRef.Key,
			},
		}})
	}

	// Webhook URL
	if instance.Spec.Webhook != nil {
		if instance.Spec.Webhook.URL != "" {
			env = append(env, corev1.EnvVar{Name: "WEBHOOK_URL", Value: instance.Spec.Webhook.URL})
		}
		if instance.Spec.Webhook.TunnelEnabled != nil && *instance.Spec.Webhook.TunnelEnabled {
			env = append(env, corev1.EnvVar{Name: "N8N_TUNNEL_ENABLED", Value: "true"})
		}
	}

	// Executions settings
	if instance.Spec.Executions != nil {
		exec := instance.Spec.Executions
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
		if exec.PruneData != nil && *exec.PruneData {
			env = append(env, corev1.EnvVar{Name: "EXECUTIONS_DATA_PRUNE", Value: "true"})
			if exec.PruneDataMaxAge != "" {
				env = append(env, corev1.EnvVar{Name: "EXECUTIONS_DATA_MAX_AGE", Value: exec.PruneDataMaxAge})
			}
		}
	}

	// Logging
	if instance.Spec.Logging != nil {
		if instance.Spec.Logging.Level != "" {
			env = append(env, corev1.EnvVar{Name: "N8N_LOG_LEVEL", Value: instance.Spec.Logging.Level})
		}
		if instance.Spec.Logging.Output != "" {
			env = append(env, corev1.EnvVar{Name: "N8N_LOG_OUTPUT", Value: instance.Spec.Logging.Output})
		}
	}

	// Metrics
	if instance.Spec.Metrics != nil && instance.Spec.Metrics.Enabled != nil && *instance.Spec.Metrics.Enabled {
		env = append(env, corev1.EnvVar{Name: "N8N_METRICS", Value: "true"})
		if instance.Spec.Metrics.Port != nil {
			env = append(env, corev1.EnvVar{Name: "N8N_METRICS_PORT", Value: fmt.Sprintf("%d", *instance.Spec.Metrics.Port)})
		}
		if instance.Spec.Metrics.IncludeWorkflowIdLabel != nil && *instance.Spec.Metrics.IncludeWorkflowIdLabel {
			env = append(env, corev1.EnvVar{Name: "N8N_METRICS_INCLUDE_WORKFLOW_ID_LABEL", Value: "true"})
		}
	}

	// Add extra env vars
	env = append(env, instance.Spec.ExtraEnv...)

	return env, nil
}

func (r *N8nInstanceReconciler) buildVolumes(instance *n8nv1alpha1.N8nInstance) ([]corev1.Volume, []corev1.VolumeMount) {
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount

	// Persistence volume
	if instance.Spec.Persistence != nil && (instance.Spec.Persistence.Enabled == nil || *instance.Spec.Persistence.Enabled) {
		pvcName := fmt.Sprintf("%s-data", instance.Name)
		if instance.Spec.Persistence.ExistingClaim != "" {
			pvcName = instance.Spec.Persistence.ExistingClaim
		}

		volumes = append(volumes, corev1.Volume{
			Name: "data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "data",
			MountPath: "/home/node/.n8n",
		})
	}

	// Add extra volumes
	volumes = append(volumes, instance.Spec.ExtraVolumes...)
	mounts = append(mounts, instance.Spec.ExtraVolumeMounts...)

	return volumes, mounts
}

func (r *N8nInstanceReconciler) configureProbes(container *corev1.Container, instance *n8nv1alpha1.N8nInstance) {
	// Default liveness probe
	container.LivenessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/healthz",
				Port: intstr.FromInt32(5678),
			},
		},
		InitialDelaySeconds: 30,
		PeriodSeconds:       10,
		TimeoutSeconds:      5,
		FailureThreshold:    6,
	}

	// Default readiness probe
	container.ReadinessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/healthz",
				Port: intstr.FromInt32(5678),
			},
		},
		InitialDelaySeconds: 5,
		PeriodSeconds:       5,
		TimeoutSeconds:      5,
		FailureThreshold:    3,
	}

	// Override with custom settings
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
	}
}

func (r *N8nInstanceReconciler) applyProbeConfig(probe *corev1.Probe, config *n8nv1alpha1.ProbeConfig) {
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
		return r.Create(ctx, desiredIngress)
	} else if err != nil {
		return err
	}

	// Update Ingress
	ingress.Spec = desiredIngress.Spec
	ingress.Annotations = desiredIngress.Annotations
	return r.Update(ctx, ingress)
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
			Labels:      r.buildLabels(instance),
			Annotations: ingressSpec.Annotations,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ingressSpec.ClassName,
			Rules: []networkingv1.IngressRule{
				{
					Host: ingressSpec.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: instance.Name,
											Port: networkingv1.ServiceBackendPort{
												Number: port,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Add TLS
	if len(ingressSpec.TLS) > 0 {
		for _, tls := range ingressSpec.TLS {
			ingress.Spec.TLS = append(ingress.Spec.TLS, networkingv1.IngressTLS{
				Hosts:      tls.Hosts,
				SecretName: tls.SecretName,
			})
		}
	}

	return ingress
}

func (r *N8nInstanceReconciler) buildLabels(instance *n8nv1alpha1.N8nInstance) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "n8n",
		"app.kubernetes.io/instance":   instance.Name,
		"app.kubernetes.io/managed-by": "n8n-operator",
	}
}

func (r *N8nInstanceReconciler) buildSelectorLabels(instance *n8nv1alpha1.N8nInstance) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":     "n8n",
		"app.kubernetes.io/instance": instance.Name,
	}
}

func (r *N8nInstanceReconciler) updateStatus(ctx context.Context, instance *n8nv1alpha1.N8nInstance, phase, message string) (ctrl.Result, error) {
	instance.Status.Phase = phase
	instance.Status.ObservedGeneration = instance.Generation

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		ObservedGeneration: instance.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             phase,
		Message:            message,
	}
	if phase == "Running" {
		condition.Status = metav1.ConditionTrue
	}

	found := false
	for i, c := range instance.Status.Conditions {
		if c.Type == condition.Type {
			instance.Status.Conditions[i] = condition
			found = true
			break
		}
	}
	if !found {
		instance.Status.Conditions = append(instance.Status.Conditions, condition)
	}

	if err := r.Status().Update(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}

	if phase == "Failed" {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *N8nInstanceReconciler) updateStatusFromDeployment(ctx context.Context, instance *n8nv1alpha1.N8nInstance) (ctrl.Result, error) {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, deploy); err != nil {
		return r.updateStatus(ctx, instance, "Pending", "Waiting for deployment")
	}

	instance.Status.Replicas = deploy.Status.Replicas
	instance.Status.ReadyReplicas = deploy.Status.ReadyReplicas

	phase := "Pending"
	message := "Deployment not ready"
	if deploy.Status.ReadyReplicas > 0 && deploy.Status.ReadyReplicas == deploy.Status.Replicas {
		phase = "Running"
		message = "All replicas ready"
	}

	// Set URL
	if instance.Spec.Ingress != nil && instance.Spec.Ingress.Enabled != nil && *instance.Spec.Ingress.Enabled && instance.Spec.Ingress.Host != "" {
		scheme := "http"
		if len(instance.Spec.Ingress.TLS) > 0 {
			scheme = "https"
		}
		instance.Status.URL = fmt.Sprintf("%s://%s", scheme, instance.Spec.Ingress.Host)
	}

	return r.updateStatus(ctx, instance, phase, message)
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
