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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

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

	mainVolumes, mainMounts := r.buildVolumes(instance, n8nComponentMain)
	if !hasVolume(mainVolumes, "data") {
		t.Fatalf("main component should include persistence volume")
	}
	if !hasMount(mainMounts, "data", "/home/node/.n8n") {
		t.Fatalf("main component should mount persistence path")
	}

	workerVolumes, workerMounts := r.buildVolumes(instance, n8nComponentWorker)
	if hasVolume(workerVolumes, "data") {
		t.Fatalf("worker component must not include persistence volume")
	}
	if hasMount(workerMounts, "data", "/home/node/.n8n") {
		t.Fatalf("worker component must not mount persistence path")
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

func int32Ptr(i int32) *int32 {
	return &i
}
