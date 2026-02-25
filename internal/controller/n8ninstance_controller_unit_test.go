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
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

func TestQueueReplicaPlanning(t *testing.T) {
	replicas := int32(4)
	workerReplicas := int32(6)
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
		Spec: n8nv1alpha1.N8nInstanceSpec{
			Replicas: &replicas,
			Queue: &n8nv1alpha1.QueueConfig{
				Enabled: boolPtr(true),
				Worker:  &n8nv1alpha1.QueueWorkerConfig{Replicas: &workerReplicas},
			},
		},
	}

	r := &N8nInstanceReconciler{}
	if got, want := r.desiredMainReplicas(instance), int32(1); got != want {
		t.Fatalf("main replicas mismatch: got %d want %d", got, want)
	}
	if got, want := r.desiredWorkerReplicas(instance), int32(6); got != want {
		t.Fatalf("worker replicas mismatch: got %d want %d", got, want)
	}
}

func TestBuildDeploymentStrategy(t *testing.T) {
	r := &N8nInstanceReconciler{}

	t.Run("defaults to RollingUpdate when unset", func(t *testing.T) {
		instance := &n8nv1alpha1.N8nInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
			Spec:       n8nv1alpha1.N8nInstanceSpec{},
		}
		strategy := r.buildDeploymentStrategy(instance)

		if got, want := strategy.Type, appsv1.RollingUpdateDeploymentStrategyType; got != want {
			t.Fatalf("strategy type mismatch: got %q want %q", got, want)
		}
		if strategy.RollingUpdate == nil {
			t.Fatalf("RollingUpdate params should be set for RollingUpdate strategy")
		}
		if strategy.RollingUpdate.MaxUnavailable.IntVal != 0 {
			t.Fatalf("MaxUnavailable should be 0, got %d", strategy.RollingUpdate.MaxUnavailable.IntVal)
		}
		if strategy.RollingUpdate.MaxSurge.IntVal != 1 {
			t.Fatalf("MaxSurge should be 1, got %d", strategy.RollingUpdate.MaxSurge.IntVal)
		}
	})

	t.Run("explicit RollingUpdate sets params", func(t *testing.T) {
		instance := &n8nv1alpha1.N8nInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
			Spec: n8nv1alpha1.N8nInstanceSpec{
				DeploymentStrategy: appsv1.RollingUpdateDeploymentStrategyType,
			},
		}
		strategy := r.buildDeploymentStrategy(instance)

		if got, want := strategy.Type, appsv1.RollingUpdateDeploymentStrategyType; got != want {
			t.Fatalf("strategy type mismatch: got %q want %q", got, want)
		}
		if strategy.RollingUpdate == nil {
			t.Fatalf("RollingUpdate params should be set for RollingUpdate strategy")
		}
	})

	t.Run("Recreate strategy has no RollingUpdate params", func(t *testing.T) {
		instance := &n8nv1alpha1.N8nInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
			Spec: n8nv1alpha1.N8nInstanceSpec{
				DeploymentStrategy: appsv1.RecreateDeploymentStrategyType,
			},
		}
		strategy := r.buildDeploymentStrategy(instance)

		if got, want := strategy.Type, appsv1.RecreateDeploymentStrategyType; got != want {
			t.Fatalf("strategy type mismatch: got %q want %q", got, want)
		}
		if strategy.RollingUpdate != nil {
			t.Fatalf("RollingUpdate params should NOT be set for Recreate strategy")
		}
	})
}

func TestBuildEnvVars_WiresAdvancedSettings(t *testing.T) {
	replicas := int32(3)
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
		Spec: n8nv1alpha1.N8nInstanceSpec{
			Replicas:        &replicas,
			Timezone:        "Europe/Berlin",
			GenericTimezone: boolPtr(false),
			Database: n8nv1alpha1.DatabaseConfig{
				Type:    "mysqldb",
				Logging: "debug",
				SecretRef: &n8nv1alpha1.SecretReference{
					Name: "mysql-secret",
				},
			},
			Queue: &n8nv1alpha1.QueueConfig{
				Enabled: boolPtr(true),
				Redis: &n8nv1alpha1.RedisConfig{
					SecretRef:    &n8nv1alpha1.SecretReference{Name: "redis-secret"},
					ClusterNodes: "redis-0:6379,redis-1:6379",
					SSL:          boolPtr(true),
					DB:           int32Ptr(2),
				},
				Health: &n8nv1alpha1.QueueHealthConfig{Active: boolPtr(true)},
			},
			Metrics: &n8nv1alpha1.MetricsConfig{
				Enabled:                       boolPtr(true),
				IncludeMessageEventBusMetrics: boolPtr(true),
			},
			License: &n8nv1alpha1.LicenseConfig{
				ActivationKeySecretRef: &n8nv1alpha1.LocalSecretKeyReference{
					Name: "n8n-license",
					Key:  "activationKey",
				},
			},
			Resources: corev1.ResourceRequirements{},
		},
	}

	r := &N8nInstanceReconciler{}
	env, err := r.buildEnvVars(context.Background(), instance, n8nComponentMain)
	if err != nil {
		t.Fatalf("buildEnvVars failed: %v", err)
	}

	assertHasEnv(t, env, "DB_MYSQLDB_HOST")
	assertHasEnv(t, env, "DB_MYSQLDB_DATABASE")
	assertHasEnv(t, env, "DB_LOGGING_ENABLED")
	assertHasEnvValue(t, env, "QUEUE_BULL_REDIS_CLUSTER_NODES", "redis-0:6379,redis-1:6379")
	assertHasEnvValue(t, env, "QUEUE_BULL_REDIS_TLS", "true")
	assertHasEnvValue(t, env, "QUEUE_HEALTH_CHECK_ACTIVE", "true")
	assertHasEnvValue(t, env, "N8N_METRICS_INCLUDE_MESSAGE_EVENT_BUS_METRICS", "true")
	assertHasEnvValue(t, env, "TZ", "Europe/Berlin")
	assertEnvSecretKey(t, env, "N8N_LICENSE_ACTIVATION_KEY", "n8n-license", "activationKey")

	if hasEnv(env, "GENERIC_TIMEZONE") {
		t.Fatalf("GENERIC_TIMEZONE should not be set when genericTimezone=false")
	}
	if hasEnv(env, "DB_POSTGRESDB_HOST") {
		t.Fatalf("DB_POSTGRESDB_HOST should not be set for mysqldb")
	}
}

func TestBuildVolumes_PersistenceMountedOnlyForMain(t *testing.T) {
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
		Spec: n8nv1alpha1.N8nInstanceSpec{
			Persistence: &n8nv1alpha1.PersistenceConfig{
				Enabled: boolPtr(true),
			},
		},
	}

	r := &N8nInstanceReconciler{}

	mainVolumes, mainMounts := r.buildVolumes(instance, n8nComponentMain, &pluginInstallPlan{})
	if !hasVolume(mainVolumes, "data") {
		t.Fatalf("main component should include persistence volume")
	}
	if !hasMount(mainMounts, "data", "/home/node/.n8n") {
		t.Fatalf("main component should mount persistence path")
	}

	workerVolumes, workerMounts := r.buildVolumes(instance, n8nComponentWorker, &pluginInstallPlan{})
	if hasVolume(workerVolumes, "data") {
		t.Fatalf("worker component must not include persistence volume")
	}
	if hasMount(workerMounts, "data", "/home/node/.n8n") {
		t.Fatalf("worker component must not mount persistence path")
	}
}

func TestBuildVolumes_PluginsMountedForMainAndWorker(t *testing.T) {
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
		Spec: n8nv1alpha1.N8nInstanceSpec{
			Database: n8nv1alpha1.DatabaseConfig{Type: "postgresdb"},
		},
	}
	plan := &pluginInstallPlan{Enabled: true}

	r := &N8nInstanceReconciler{}

	mainVolumes, mainMounts := r.buildVolumes(instance, n8nComponentMain, plan)
	if !hasVolume(mainVolumes, pluginVolumeName) {
		t.Fatalf("main component should include plugin volume")
	}
	if !hasMount(mainMounts, pluginVolumeName, pluginVolumeMountPath) {
		t.Fatalf("main component should mount plugin path")
	}
	if !hasEmptyDirVolume(mainVolumes, pluginVolumeName) {
		t.Fatalf("main component should use pod-local plugin volume when shared cache is disabled")
	}

	workerVolumes, workerMounts := r.buildVolumes(instance, n8nComponentWorker, plan)
	if !hasVolume(workerVolumes, pluginVolumeName) {
		t.Fatalf("worker component should include plugin volume")
	}
	if !hasMount(workerMounts, pluginVolumeName, pluginVolumeMountPath) {
		t.Fatalf("worker component should mount plugin path")
	}
	if !hasEmptyDirVolume(workerVolumes, pluginVolumeName) {
		t.Fatalf("worker component should use pod-local plugin volume when shared cache is disabled")
	}
}

func TestBuildVolumes_PluginsUsePVCWhenSharedCacheEnabled(t *testing.T) {
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
		Spec: n8nv1alpha1.N8nInstanceSpec{
			Database: n8nv1alpha1.DatabaseConfig{Type: "postgresdb"},
		},
	}
	plan := &pluginInstallPlan{Enabled: true, UseSharedCache: true}

	r := &N8nInstanceReconciler{}

	volumes, mounts := r.buildVolumes(instance, n8nComponentMain, plan)
	if !hasMount(mounts, pluginVolumeName, pluginVolumeMountPath) {
		t.Fatalf("main component should mount plugin path")
	}
	if !hasPVCVolume(volumes, pluginVolumeName, pluginPVCName(instance)) {
		t.Fatalf("main component should use PVC plugin volume when shared cache is enabled")
	}
}

func TestShouldUseSharedPluginCache(t *testing.T) {
	reconciler := &N8nInstanceReconciler{}

	noPersistence := &n8nv1alpha1.N8nInstance{}
	if reconciler.shouldUseSharedPluginCache(noPersistence) {
		t.Fatalf("shared cache should be disabled without persistence access modes")
	}

	rwo := &n8nv1alpha1.N8nInstance{
		Spec: n8nv1alpha1.N8nInstanceSpec{
			Persistence: &n8nv1alpha1.PersistenceConfig{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			},
		},
	}
	if reconciler.shouldUseSharedPluginCache(rwo) {
		t.Fatalf("shared cache should be disabled for RWO access mode")
	}

	rwx := &n8nv1alpha1.N8nInstance{
		Spec: n8nv1alpha1.N8nInstanceSpec{
			Persistence: &n8nv1alpha1.PersistenceConfig{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			},
		},
	}
	if !reconciler.shouldUseSharedPluginCache(rwx) {
		t.Fatalf("shared cache should be enabled for RWX access mode")
	}
}

func TestIsServiceMonitorUnavailable(t *testing.T) {
	noMatch := &apimeta.NoKindMatchError{
		GroupKind:        schema.GroupKind{Group: "monitoring.coreos.com", Kind: "ServiceMonitor"},
		SearchedVersions: []string{"v1"},
	}
	if !isServiceMonitorUnavailable(noMatch) {
		t.Fatalf("expected no-match errors to be treated as ServiceMonitor unavailable")
	}
	if isServiceMonitorUnavailable(context.DeadlineExceeded) {
		t.Fatalf("unexpected true for unrelated error")
	}
}

func TestUpdateStatusFields_NoStatusWriteWhenUnchanged(t *testing.T) {
	transition := metav1.NewTime(time.Unix(1700000000, 0))
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "n8n",
			Namespace:  "services",
			Generation: 7,
		},
		Status: n8nv1alpha1.N8nInstanceStatus{
			Phase:               "Running",
			Replicas:            1,
			ReadyReplicas:       1,
			WorkerReplicas:      0,
			ReadyWorkerReplicas: 0,
			URL:                 "http://n8n.services.svc:5678",
			ObservedGeneration:  7,
			Conditions: []metav1.Condition{{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 7,
				LastTransitionTime: transition,
				Reason:             "Running",
				Message:            "All managed deployments are ready",
			}},
		},
	}

	reconciler := &N8nInstanceReconciler{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic, status update should be skipped when unchanged: %v", r)
		}
	}()

	result, err := reconciler.updateStatusFields(
		context.Background(),
		instance,
		"Running",
		"All managed deployments are ready",
		1,
		1,
		0,
		0,
		"http://n8n.services.svc:5678",
	)
	if err != nil {
		t.Fatalf("updateStatusFields returned error: %v", err)
	}
	if result.RequeueAfter != 5*time.Minute {
		t.Fatalf("unexpected requeue: got %s want %s", result.RequeueAfter, 5*time.Minute)
	}

	got := apimeta.FindStatusCondition(instance.Status.Conditions, "Ready")
	if got == nil {
		t.Fatalf("missing Ready condition")
	}
	if !got.LastTransitionTime.Equal(&transition) {
		t.Fatalf("Ready LastTransitionTime changed unexpectedly: got %s want %s", got.LastTransitionTime.Time, transition.Time)
	}
}

func TestOwnerSetupEnabled(t *testing.T) {
	instance := &n8nv1alpha1.N8nInstance{}
	reconciler := &N8nInstanceReconciler{}

	if reconciler.ownerSetupEnabled(instance) {
		t.Fatalf("owner setup should be disabled when config is missing")
	}

	instance.Spec.OwnerSetup = &n8nv1alpha1.OwnerSetupConfig{
		SecretRef: &n8nv1alpha1.SecretReference{Name: "owner-secret"},
	}
	if !reconciler.ownerSetupEnabled(instance) {
		t.Fatalf("owner setup should be enabled when secretRef is configured")
	}

	instance.Spec.OwnerSetup.Enabled = boolPtr(false)
	if reconciler.ownerSetupEnabled(instance) {
		t.Fatalf("owner setup should be disabled when enabled=false")
	}
}

func TestBuildOwnerSetupJob_Defaults(t *testing.T) {
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "n8n", Namespace: "services"},
		Spec: n8nv1alpha1.N8nInstanceSpec{
			Database: n8nv1alpha1.DatabaseConfig{Type: "postgresdb"},
			OwnerSetup: &n8nv1alpha1.OwnerSetupConfig{
				SecretRef: &n8nv1alpha1.SecretReference{Name: "n8n-owner"},
			},
		},
	}

	reconciler := &N8nInstanceReconciler{}
	reconciler.setDefaults(instance)
	job := reconciler.buildOwnerSetupJob(instance)

	if got, want := job.Name, "n8n-owner-setup"; got != want {
		t.Fatalf("job name mismatch: got %q want %q", got, want)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != 3600 {
		t.Fatalf("unexpected job TTL: %+v", job.Spec.TTLSecondsAfterFinished)
	}
	if len(job.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected one container, got %d", len(job.Spec.Template.Spec.Containers))
	}

	container := job.Spec.Template.Spec.Containers[0]
	if got, want := container.Image, "curlimages/curl:8.12.1"; got != want {
		t.Fatalf("job image mismatch: got %q want %q", got, want)
	}

	assertHasEnvValue(t, container.Env, "N8N_BASE_URL", "http://n8n.services.svc:5678")
	assertHasEnvValue(t, container.Env, "N8N_REST_ENDPOINT", "rest")
	assertEnvSecretKey(t, container.Env, "OWNER_EMAIL", "n8n-owner", "email")
	assertEnvSecretKey(t, container.Env, "OWNER_FIRST_NAME", "n8n-owner", "firstName")
	assertEnvSecretKey(t, container.Env, "OWNER_LAST_NAME", "n8n-owner", "lastName")
	assertEnvSecretKey(t, container.Env, "OWNER_PASSWORD", "n8n-owner", "password")
}

func TestOwnerSetupJobName_TruncatesLongNames(t *testing.T) {
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n8n-instance-name-that-is-way-too-long-for-a-kubernetes-job-name-limit-check",
		},
	}

	got := ownerSetupJobName(instance)
	if len(got) > 63 {
		t.Fatalf("owner setup job name exceeded 63 chars: %d (%q)", len(got), got)
	}
	if !strings.HasSuffix(got, "-owner-setup") {
		t.Fatalf("owner setup job name must end with -owner-setup, got %q", got)
	}
}

func TestSyncOwnerSetupCondition_Succeeded(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := n8nv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register n8n scheme: %v", err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register batch scheme: %v", err)
	}

	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "n8n",
			Namespace:  "services",
			Generation: 4,
		},
		Spec: n8nv1alpha1.N8nInstanceSpec{
			OwnerSetup: &n8nv1alpha1.OwnerSetupConfig{
				SecretRef: &n8nv1alpha1.SecretReference{Name: "n8n-owner"},
			},
		},
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ownerSetupJobName(instance),
			Namespace: instance.Namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: int32Ptr(6),
		},
		Status: batchv1.JobStatus{
			Succeeded: 1,
		},
	}

	reconciler := &N8nInstanceReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(job).Build(),
	}

	reconciler.syncOwnerSetupCondition(context.Background(), instance)

	condition := apimeta.FindStatusCondition(instance.Status.Conditions, ownerSetupConditionType)
	if condition == nil {
		t.Fatalf("missing %s condition", ownerSetupConditionType)
	}
	if condition.Status != metav1.ConditionTrue {
		t.Fatalf("unexpected condition status: got %s want %s", condition.Status, metav1.ConditionTrue)
	}
	if got, want := condition.Reason, "Succeeded"; got != want {
		t.Fatalf("unexpected condition reason: got %q want %q", got, want)
	}
	if got, want := condition.ObservedGeneration, int64(4); got != want {
		t.Fatalf("unexpected observed generation: got %d want %d", got, want)
	}
}

func TestSyncOwnerSetupCondition_RemovedWhenDisabled(t *testing.T) {
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "n8n",
			Namespace:  "services",
			Generation: 2,
		},
		Status: n8nv1alpha1.N8nInstanceStatus{
			Conditions: []metav1.Condition{{
				Type:               ownerSetupConditionType,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: 1,
				Reason:             "Succeeded",
				Message:            "Owner setup Job completed successfully",
			}},
		},
	}

	reconciler := &N8nInstanceReconciler{}
	reconciler.syncOwnerSetupCondition(context.Background(), instance)

	if apimeta.FindStatusCondition(instance.Status.Conditions, ownerSetupConditionType) != nil {
		t.Fatalf("expected %s condition to be removed when owner setup is disabled", ownerSetupConditionType)
	}
}

func TestSetPluginStatusResolved_WithPlugins(t *testing.T) {
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "n8n",
			Namespace:  "services",
			Generation: 3,
		},
	}

	plan := &pluginInstallPlan{
		Enabled:      true,
		Hash:         "deadbeef",
		PackageSpecs: []string{"n8n-nodes-a@1.0.0", "n8n-nodes-b@2.0.0"},
	}

	reconciler := &N8nInstanceReconciler{}
	reconciler.setPluginStatusResolved(instance, plan)

	if got, want := instance.Status.PluginCount, int32(2); got != want {
		t.Fatalf("unexpected plugin count: got %d want %d", got, want)
	}
	if got, want := instance.Status.PluginHash, "deadbeef"; got != want {
		t.Fatalf("unexpected plugin hash: got %q want %q", got, want)
	}
	if len(instance.Status.PluginPackages) != 2 {
		t.Fatalf("unexpected plugin package count: %d", len(instance.Status.PluginPackages))
	}

	condition := apimeta.FindStatusCondition(instance.Status.Conditions, pluginsConditionType)
	if condition == nil {
		t.Fatalf("missing %s condition", pluginsConditionType)
	}
	if condition.Status != metav1.ConditionTrue {
		t.Fatalf("unexpected condition status: got %s want %s", condition.Status, metav1.ConditionTrue)
	}
	if got, want := condition.Reason, "Resolved"; got != want {
		t.Fatalf("unexpected condition reason: got %q want %q", got, want)
	}
}

func TestSetPluginStatusResolved_NoPlugins(t *testing.T) {
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "n8n",
			Namespace:  "services",
			Generation: 5,
		},
		Status: n8nv1alpha1.N8nInstanceStatus{
			PluginCount:    1,
			PluginHash:     "abc",
			PluginPackages: []string{"n8n-nodes-old@1.0.0"},
		},
	}

	reconciler := &N8nInstanceReconciler{}
	reconciler.setPluginStatusResolved(instance, &pluginInstallPlan{})

	if instance.Status.PluginCount != 0 {
		t.Fatalf("expected plugin count to be cleared")
	}
	if instance.Status.PluginHash != "" {
		t.Fatalf("expected plugin hash to be cleared")
	}
	if len(instance.Status.PluginPackages) != 0 {
		t.Fatalf("expected plugin packages to be cleared")
	}

	condition := apimeta.FindStatusCondition(instance.Status.Conditions, pluginsConditionType)
	if condition == nil {
		t.Fatalf("missing %s condition", pluginsConditionType)
	}
	if condition.Status != metav1.ConditionTrue {
		t.Fatalf("unexpected condition status: got %s want %s", condition.Status, metav1.ConditionTrue)
	}
	if got, want := condition.Reason, "NoPlugins"; got != want {
		t.Fatalf("unexpected condition reason: got %q want %q", got, want)
	}
}

func TestSetPluginStatusError_SetsConditionFalse(t *testing.T) {
	instance := &n8nv1alpha1.N8nInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "n8n",
			Namespace:  "services",
			Generation: 8,
		},
		Status: n8nv1alpha1.N8nInstanceStatus{
			PluginCount:    2,
			PluginHash:     "hash",
			PluginPackages: []string{"n8n-nodes-a@1.0.0", "n8n-nodes-b@2.0.0"},
		},
	}

	reconciler := &N8nInstanceReconciler{}
	reconciler.setPluginStatusError(instance, context.DeadlineExceeded)

	condition := apimeta.FindStatusCondition(instance.Status.Conditions, pluginsConditionType)
	if condition == nil {
		t.Fatalf("missing %s condition", pluginsConditionType)
	}
	if condition.Status != metav1.ConditionFalse {
		t.Fatalf("unexpected condition status: got %s want %s", condition.Status, metav1.ConditionFalse)
	}
	if got, want := condition.Reason, "ResolveFailed"; got != want {
		t.Fatalf("unexpected condition reason: got %q want %q", got, want)
	}
	if instance.Status.PluginCount != 2 {
		t.Fatalf("plugin count should remain unchanged on resolve error")
	}
}

func assertHasEnv(t *testing.T, env []corev1.EnvVar, name string) {
	t.Helper()
	if !hasEnv(env, name) {
		t.Fatalf("missing env %s", name)
	}
}

func assertHasEnvValue(t *testing.T, env []corev1.EnvVar, name, expected string) {
	t.Helper()
	for i := range env {
		if env[i].Name == name {
			if env[i].Value != expected {
				t.Fatalf("env %s mismatch: got %q want %q", name, env[i].Value, expected)
			}
			return
		}
	}
	t.Fatalf("missing env %s", name)
}

func hasEnv(env []corev1.EnvVar, name string) bool {
	for i := range env {
		if env[i].Name == name {
			return true
		}
	}
	return false
}

func assertEnvSecretKey(t *testing.T, env []corev1.EnvVar, envName, secretName, key string) {
	t.Helper()
	for i := range env {
		if env[i].Name != envName {
			continue
		}
		if env[i].ValueFrom == nil || env[i].ValueFrom.SecretKeyRef == nil {
			t.Fatalf("env %s is not sourced from secret key", envName)
		}
		ref := env[i].ValueFrom.SecretKeyRef
		if ref.Name != secretName || ref.Key != key {
			t.Fatalf("env %s secret ref mismatch: got %s/%s want %s/%s", envName, ref.Name, ref.Key, secretName, key)
		}
		if ref.Optional == nil || *ref.Optional {
			t.Fatalf("env %s secret ref should be required", envName)
		}
		return
	}
	t.Fatalf("missing env %s", envName)
}

func hasVolume(volumes []corev1.Volume, name string) bool {
	for i := range volumes {
		if volumes[i].Name == name {
			return true
		}
	}
	return false
}

func hasMount(mounts []corev1.VolumeMount, name, path string) bool {
	for i := range mounts {
		if mounts[i].Name == name && mounts[i].MountPath == path {
			return true
		}
	}
	return false
}

func hasEmptyDirVolume(volumes []corev1.Volume, name string) bool {
	for i := range volumes {
		if volumes[i].Name == name && volumes[i].EmptyDir != nil {
			return true
		}
	}
	return false
}

func hasPVCVolume(volumes []corev1.Volume, name, claimName string) bool {
	for i := range volumes {
		if volumes[i].Name == name &&
			volumes[i].PersistentVolumeClaim != nil &&
			volumes[i].PersistentVolumeClaim.ClaimName == claimName {
			return true
		}
	}
	return false
}

func int32Ptr(i int32) *int32 {
	return &i
}
