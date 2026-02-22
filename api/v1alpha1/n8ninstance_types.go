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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// N8nInstanceSpec defines the desired state of N8nInstance.
type N8nInstanceSpec struct {
	// Replicas is the number of n8n replicas to deploy.
	// For production with queue mode, use multiple replicas.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Image is the n8n container image to deploy.
	// +kubebuilder:default="docker.n8n.io/n8nio/n8n:latest"
	// +optional
	Image string `json:"image,omitempty"`

	// ImagePullPolicy defines when to pull the image.
	// +kubebuilder:default="IfNotPresent"
	// +kubebuilder:validation:Enum=Always;Never;IfNotPresent
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ImagePullSecrets is a list of secrets for pulling from private registries.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Database configures the database connection.
	// +kubebuilder:validation:Required
	Database DatabaseConfig `json:"database"`

	// Queue configures queue mode for scaling (optional, enables multiple workers).
	// +optional
	Queue *QueueConfig `json:"queue,omitempty"`

	// Encryption configures encryption keys for credentials.
	// +optional
	Encryption *EncryptionConfig `json:"encryption,omitempty"`

	// Webhook configures webhook settings.
	// +optional
	Webhook *WebhookConfig `json:"webhook,omitempty"`

	// SMTP configures email sending.
	// +optional
	SMTP *SMTPConfig `json:"smtp,omitempty"`

	// ExternalHooks configures external hooks file.
	// +optional
	ExternalHooks *ExternalHooksConfig `json:"externalHooks,omitempty"`

	// Executions configures execution settings.
	// +optional
	Executions *ExecutionsConfig `json:"executions,omitempty"`

	// Logging configures logging settings.
	// +optional
	Logging *LoggingConfig `json:"logging,omitempty"`

	// Timezone sets the default timezone.
	// +kubebuilder:default="UTC"
	// +optional
	Timezone string `json:"timezone,omitempty"`

	// GenericTimezone enables generic timezone support.
	// +kubebuilder:default=true
	// +optional
	GenericTimezone *bool `json:"genericTimezone,omitempty"`

	// Service configures the Kubernetes Service.
	// +optional
	Service *ServiceConfig `json:"service,omitempty"`

	// Ingress configures the Kubernetes Ingress.
	// +optional
	Ingress *IngressConfig `json:"ingress,omitempty"`

	// Resources configures container resource requirements.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// NodeSelector for pod scheduling.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations for pod scheduling.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Affinity for pod scheduling.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// SecurityContext for the pod.
	// +optional
	PodSecurityContext *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`

	// SecurityContext for the container.
	// +optional
	SecurityContext *corev1.SecurityContext `json:"securityContext,omitempty"`

	// Persistence configures persistent storage for n8n data.
	// +optional
	Persistence *PersistenceConfig `json:"persistence,omitempty"`

	// ExtraEnv allows setting additional environment variables.
	// +optional
	ExtraEnv []corev1.EnvVar `json:"extraEnv,omitempty"`

	// ExtraEnvFrom allows setting additional environment variables from ConfigMaps/Secrets.
	// +optional
	ExtraEnvFrom []corev1.EnvFromSource `json:"extraEnvFrom,omitempty"`

	// ExtraVolumes allows mounting additional volumes.
	// +optional
	ExtraVolumes []corev1.Volume `json:"extraVolumes,omitempty"`

	// ExtraVolumeMounts allows mounting additional volume mounts.
	// +optional
	ExtraVolumeMounts []corev1.VolumeMount `json:"extraVolumeMounts,omitempty"`

	// PodAnnotations to add to pods.
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// PodLabels to add to pods.
	// +optional
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// ServiceAccountName to use for pods.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// InitContainers to run before the main container.
	// +optional
	InitContainers []corev1.Container `json:"initContainers,omitempty"`

	// SidecarContainers to run alongside the main container.
	// +optional
	SidecarContainers []corev1.Container `json:"sidecarContainers,omitempty"`

	// Metrics configures Prometheus metrics.
	// +optional
	Metrics *MetricsConfig `json:"metrics,omitempty"`

	// HealthCheck configures health check settings.
	// +optional
	HealthCheck *HealthCheckConfig `json:"healthCheck,omitempty"`

	// OwnerSetup configures one-time setup of the first n8n owner account.
	// +optional
	OwnerSetup *OwnerSetupConfig `json:"ownerSetup,omitempty"`
}

// DatabaseConfig configures the database connection.
type DatabaseConfig struct {
	// Type is the database type: sqlite, postgresdb, mariadb, mysqldb.
	// +kubebuilder:validation:Enum=sqlite;postgresdb;mariadb;mysqldb
	// +kubebuilder:default="postgresdb"
	Type string `json:"type"`

	// SecretRef references a Secret containing database connection details.
	// Expected keys: host, port, database, user, password (or connection-url).
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`

	// SSL enables SSL connection to the database.
	// +optional
	SSL *bool `json:"ssl,omitempty"`

	// SSLRejectUnauthorized rejects unauthorized SSL certificates.
	// +kubebuilder:default=true
	// +optional
	SSLRejectUnauthorized *bool `json:"sslRejectUnauthorized,omitempty"`

	// TablePrefix adds a prefix to all database tables.
	// +optional
	TablePrefix string `json:"tablePrefix,omitempty"`

	// Logging enables database query logging.
	// +kubebuilder:validation:Enum=none;error;warn;info;debug
	// +kubebuilder:default="none"
	// +optional
	Logging string `json:"logging,omitempty"`
}

// QueueConfig configures queue mode for horizontal scaling.
type QueueConfig struct {
	// Enabled enables queue mode with Redis.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Redis configures the Redis connection for queue mode.
	// +optional
	Redis *RedisConfig `json:"redis,omitempty"`

	// BullMQ configures BullMQ settings.
	// +optional
	BullMQ *BullMQConfig `json:"bullmq,omitempty"`

	// Health configures queue health checks.
	// +optional
	Health *QueueHealthConfig `json:"health,omitempty"`

	// Worker configures dedicated queue worker processes.
	// +optional
	Worker *QueueWorkerConfig `json:"worker,omitempty"`
}

// RedisConfig configures Redis connection.
type RedisConfig struct {
	// SecretRef references a Secret containing Redis connection details.
	// Expected keys: host, port, password (optional), db (optional).
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`

	// ClusterNodes for Redis Cluster mode (comma-separated host:port pairs).
	// +optional
	ClusterNodes string `json:"clusterNodes,omitempty"`

	// SSL enables SSL connection to Redis.
	// +optional
	SSL *bool `json:"ssl,omitempty"`

	// DB selects the Redis logical database.
	// +kubebuilder:validation:Minimum=0
	// +optional
	DB *int32 `json:"db,omitempty"`
}

// BullMQConfig configures BullMQ settings.
type BullMQConfig struct {
	// Prefix for BullMQ queue names.
	// +kubebuilder:default="bull"
	// +optional
	Prefix string `json:"prefix,omitempty"`

	// GracefulShutdownTimeout in milliseconds.
	// +kubebuilder:default=30000
	// +optional
	GracefulShutdownTimeout *int32 `json:"gracefulShutdownTimeout,omitempty"`
}

// QueueHealthConfig configures queue health checks.
type QueueHealthConfig struct {
	// Active enables active queue health checks.
	// +kubebuilder:default=false
	// +optional
	Active *bool `json:"active,omitempty"`
}

// QueueWorkerConfig configures dedicated queue workers.
type QueueWorkerConfig struct {
	// Enabled enables dedicated worker Deployment management.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Replicas is the number of worker replicas.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`
}

// EncryptionConfig configures encryption settings.
type EncryptionConfig struct {
	// KeySecretRef references a Secret containing the encryption key.
	// Expected key: key (32+ character random string).
	// +optional
	KeySecretRef *SecretKeyReference `json:"keySecretRef,omitempty"`
}

// WebhookConfig configures webhook settings.
type WebhookConfig struct {
	// URL is the public URL for webhooks.
	// +optional
	URL string `json:"url,omitempty"`

	// TunnelEnabled enables the n8n tunnel for webhooks.
	// +kubebuilder:default=false
	// +optional
	TunnelEnabled *bool `json:"tunnelEnabled,omitempty"`

	// Path prefix for webhook URLs.
	// +optional
	Path string `json:"path,omitempty"`
}

// SMTPConfig configures SMTP for sending emails.
type SMTPConfig struct {
	// Enabled enables SMTP.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// SecretRef references a Secret containing SMTP credentials.
	// Expected keys: host, port, user, password, sender.
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`

	// SSL enables SSL for SMTP.
	// +optional
	SSL *bool `json:"ssl,omitempty"`
}

// OwnerSetupConfig configures initial owner bootstrap.
type OwnerSetupConfig struct {
	// Enabled enables owner bootstrap.
	// If unset, owner bootstrap is enabled when SecretRef is configured.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// SecretRef references a Secret containing owner setup values.
	// Expected keys: email, firstName, lastName, password.
	// +optional
	SecretRef *SecretReference `json:"secretRef,omitempty"`

	// ServiceName overrides the Service used to reach n8n.
	// Defaults to the N8nInstance name.
	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// RestEndpoint is the n8n REST endpoint path segment.
	// Defaults to "rest".
	// +kubebuilder:default="rest"
	// +optional
	RestEndpoint string `json:"restEndpoint,omitempty"`

	// JobImage is the image used by the owner setup bootstrap Job.
	// +kubebuilder:default="curlimages/curl:8.12.1"
	// +optional
	JobImage string `json:"jobImage,omitempty"`

	// JobTTLSecondsAfterFinished controls Job cleanup after completion.
	// +kubebuilder:default=3600
	// +kubebuilder:validation:Minimum=0
	// +optional
	JobTTLSecondsAfterFinished *int32 `json:"jobTTLSecondsAfterFinished,omitempty"`
}

// ExternalHooksConfig configures external hooks.
type ExternalHooksConfig struct {
	// Files is a comma-separated list of hook files to load.
	// +optional
	Files string `json:"files,omitempty"`
}

// ExecutionsConfig configures execution settings.
type ExecutionsConfig struct {
	// Mode is the execution mode: regular, queue.
	// +kubebuilder:validation:Enum=regular;queue
	// +kubebuilder:default="regular"
	// +optional
	Mode string `json:"mode,omitempty"`

	// Timeout is the max execution time in seconds (0 = no limit).
	// +kubebuilder:default=-1
	// +optional
	Timeout *int32 `json:"timeout,omitempty"`

	// MaxTimeout is the max timeout that can be set in seconds.
	// +kubebuilder:default=3600
	// +optional
	MaxTimeout *int32 `json:"maxTimeout,omitempty"`

	// SaveDataOnError saves execution data on error.
	// +kubebuilder:validation:Enum=all;none
	// +kubebuilder:default="all"
	// +optional
	SaveDataOnError string `json:"saveDataOnError,omitempty"`

	// SaveDataOnSuccess saves execution data on success.
	// +kubebuilder:validation:Enum=all;none
	// +kubebuilder:default="all"
	// +optional
	SaveDataOnSuccess string `json:"saveDataOnSuccess,omitempty"`

	// SaveManualExecutions saves manual execution data.
	// +kubebuilder:default=true
	// +optional
	SaveManualExecutions *bool `json:"saveManualExecutions,omitempty"`

	// PruneData enables automatic pruning of old executions.
	// +kubebuilder:default=true
	// +optional
	PruneData *bool `json:"pruneData,omitempty"`

	// PruneDataMaxAge is the max age of executions to keep (e.g., "336h" for 14 days).
	// +kubebuilder:default="336h"
	// +optional
	PruneDataMaxAge string `json:"pruneDataMaxAge,omitempty"`

	// PruneDataMaxCount is the max number of executions to keep.
	// +kubebuilder:default=10000
	// +optional
	PruneDataMaxCount *int32 `json:"pruneDataMaxCount,omitempty"`
}

// LoggingConfig configures logging.
type LoggingConfig struct {
	// Level sets the log level.
	// +kubebuilder:validation:Enum=error;warn;info;debug;verbose
	// +kubebuilder:default="info"
	// +optional
	Level string `json:"level,omitempty"`

	// Output sets the log output format.
	// +kubebuilder:validation:Enum=console;file
	// +kubebuilder:default="console"
	// +optional
	Output string `json:"output,omitempty"`
}

// ServiceConfig configures the Kubernetes Service.
type ServiceConfig struct {
	// Type is the Service type.
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default="ClusterIP"
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`

	// Port is the Service port.
	// +kubebuilder:default=5678
	// +optional
	Port int32 `json:"port,omitempty"`

	// NodePort is the NodePort (if type is NodePort or LoadBalancer).
	// +optional
	NodePort *int32 `json:"nodePort,omitempty"`

	// Annotations to add to the Service.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Labels to add to the Service.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// IngressConfig configures the Kubernetes Ingress.
type IngressConfig struct {
	// Enabled enables the Ingress.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// ClassName is the IngressClass name.
	// +optional
	ClassName *string `json:"className,omitempty"`

	// Host is the hostname for the Ingress.
	// +optional
	Host string `json:"host,omitempty"`

	// Path is the path for the Ingress.
	// +kubebuilder:default="/"
	// +optional
	Path string `json:"path,omitempty"`

	// PathType is the path type for the Ingress.
	// +kubebuilder:default="Prefix"
	// +optional
	PathType string `json:"pathType,omitempty"`

	// Annotations to add to the Ingress.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// TLS configures TLS for the Ingress.
	// +optional
	TLS []IngressTLS `json:"tls,omitempty"`
}

// IngressTLS configures TLS for Ingress.
type IngressTLS struct {
	// Hosts is a list of hosts for this TLS certificate.
	Hosts []string `json:"hosts,omitempty"`
	// SecretName is the name of the Secret containing the TLS certificate.
	SecretName string `json:"secretName,omitempty"`
}

// PersistenceConfig configures persistent storage.
type PersistenceConfig struct {
	// Enabled enables persistent storage.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// StorageClass is the storage class name.
	// +optional
	StorageClass *string `json:"storageClass,omitempty"`

	// Size is the storage size.
	// +kubebuilder:default="1Gi"
	// +optional
	Size string `json:"size,omitempty"`

	// AccessModes are the PVC access modes.
	// +optional
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`

	// ExistingClaim is the name of an existing PVC to use.
	// +optional
	ExistingClaim string `json:"existingClaim,omitempty"`
}

// MetricsConfig configures Prometheus metrics.
type MetricsConfig struct {
	// Enabled enables Prometheus metrics.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Port is the metrics port.
	// +kubebuilder:default=5679
	// +optional
	Port *int32 `json:"port,omitempty"`

	// IncludeWorkflowIdLabel includes workflow_id label in metrics.
	// +kubebuilder:default=false
	// +optional
	IncludeWorkflowIdLabel *bool `json:"includeWorkflowIdLabel,omitempty"`

	// IncludeNodeTypeLabel includes node_type label in metrics.
	// +kubebuilder:default=false
	// +optional
	IncludeNodeTypeLabel *bool `json:"includeNodeTypeLabel,omitempty"`

	// IncludeCredentialTypeLabel includes credential_type label in metrics.
	// +kubebuilder:default=false
	// +optional
	IncludeCredentialTypeLabel *bool `json:"includeCredentialTypeLabel,omitempty"`

	// IncludeApiEndpoints includes API endpoint metrics.
	// +kubebuilder:default=false
	// +optional
	IncludeApiEndpoints *bool `json:"includeApiEndpoints,omitempty"`

	// IncludeMessageEventBusMetrics includes message event bus metrics.
	// +kubebuilder:default=false
	// +optional
	IncludeMessageEventBusMetrics *bool `json:"includeMessageEventBusMetrics,omitempty"`

	// ServiceMonitor configures a Prometheus ServiceMonitor.
	// +optional
	ServiceMonitor *ServiceMonitorConfig `json:"serviceMonitor,omitempty"`
}

// ServiceMonitorConfig configures a Prometheus ServiceMonitor.
type ServiceMonitorConfig struct {
	// Enabled enables the ServiceMonitor.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Interval is the scrape interval.
	// +kubebuilder:default="30s"
	// +optional
	Interval string `json:"interval,omitempty"`

	// Labels to add to the ServiceMonitor.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// HealthCheckConfig configures health check settings.
type HealthCheckConfig struct {
	// LivenessProbe configures the liveness probe.
	// +optional
	LivenessProbe *ProbeConfig `json:"livenessProbe,omitempty"`

	// ReadinessProbe configures the readiness probe.
	// +optional
	ReadinessProbe *ProbeConfig `json:"readinessProbe,omitempty"`

	// StartupProbe configures the startup probe.
	// +optional
	StartupProbe *ProbeConfig `json:"startupProbe,omitempty"`
}

// ProbeConfig configures a probe.
type ProbeConfig struct {
	// Enabled enables the probe.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// InitialDelaySeconds is the initial delay.
	// +optional
	InitialDelaySeconds *int32 `json:"initialDelaySeconds,omitempty"`

	// PeriodSeconds is the period between probes.
	// +optional
	PeriodSeconds *int32 `json:"periodSeconds,omitempty"`

	// TimeoutSeconds is the probe timeout.
	// +optional
	TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`

	// SuccessThreshold is the success threshold.
	// +optional
	SuccessThreshold *int32 `json:"successThreshold,omitempty"`

	// FailureThreshold is the failure threshold.
	// +optional
	FailureThreshold *int32 `json:"failureThreshold,omitempty"`
}

// N8nInstanceStatus defines the observed state of N8nInstance.
type N8nInstanceStatus struct {
	// Phase represents the current phase of the n8n instance.
	// +kubebuilder:validation:Enum=Pending;Progressing;Running;Failed;Unknown
	// +optional
	Phase string `json:"phase,omitempty"`

	// Replicas is the number of replicas currently running.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// ReadyReplicas is the number of ready replicas.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// WorkerReplicas is the number of worker replicas currently running.
	// +optional
	WorkerReplicas int32 `json:"workerReplicas,omitempty"`

	// ReadyWorkerReplicas is the number of ready worker replicas.
	// +optional
	ReadyWorkerReplicas int32 `json:"readyWorkerReplicas,omitempty"`

	// URL is the URL where n8n is accessible.
	// +optional
	URL string `json:"url,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Workers",type=integer,JSONPath=`.status.readyWorkerReplicas`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.url`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// N8nInstance is the Schema for the n8ninstances API.
type N8nInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   N8nInstanceSpec   `json:"spec,omitempty"`
	Status N8nInstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// N8nInstanceList contains a list of N8nInstance.
type N8nInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []N8nInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&N8nInstance{}, &N8nInstanceList{})
}
