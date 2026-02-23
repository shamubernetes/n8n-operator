package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

const pluginInstallerScript = `
set -eu

plugin_dir="` + pluginVolumeMountPath + `"
hash_file="${plugin_dir}/.plugin-hash"
lock_dir="${plugin_dir}/.install-lock"
lock_heartbeat_file="${lock_dir}/heartbeat"
max_wait_seconds=7200
stale_lock_seconds=900

mkdir -p "${plugin_dir}"

if [ -f "${hash_file}" ] && [ "$(cat "${hash_file}")" = "${PLUGIN_HASH}" ]; then
	echo "Plugins already up to date"
	exit 0
fi

waited=0
missing_heartbeat_wait=0
while ! mkdir "${lock_dir}" 2>/dev/null; do
	if [ -f "${hash_file}" ] && [ "$(cat "${hash_file}")" = "${PLUGIN_HASH}" ]; then
		echo "Plugins were installed by another pod"
		exit 0
	fi

	if [ -f "${lock_heartbeat_file}" ]; then
		now="$(date +%s)"
		last_heartbeat="$(cat "${lock_heartbeat_file}" 2>/dev/null || printf '0')"
		case "${last_heartbeat}" in
			''|*[!0-9]*) last_heartbeat=0 ;;
		esac
		if [ $((now - last_heartbeat)) -gt "${stale_lock_seconds}" ]; then
			echo "Removing stale plugin installation lock"
			rm -rf "${lock_dir}" || true
			continue
		fi
		missing_heartbeat_wait=0
	else
		missing_heartbeat_wait=$((missing_heartbeat_wait + 2))
		if [ "${missing_heartbeat_wait}" -gt "${stale_lock_seconds}" ]; then
			echo "Removing stale plugin installation lock without heartbeat"
			rm -rf "${lock_dir}" || true
			missing_heartbeat_wait=0
			continue
		fi
	fi

	waited=$((waited + 2))
	if [ "${waited}" -ge "${max_wait_seconds}" ]; then
		echo "Timed out waiting for plugin installation lock"
		exit 1
	fi
	sleep 2
done

printf '%s' "$(date +%s)" > "${lock_heartbeat_file}"
(
	while true; do
		sleep 30
		printf '%s' "$(date +%s)" > "${lock_heartbeat_file}" || exit 0
	done
) &
heartbeat_pid="$!"

cleanup() {
	if [ -n "${heartbeat_pid}" ]; then
		kill "${heartbeat_pid}" 2>/dev/null || true
	fi
	rm -rf "${lock_dir}" || true
}
trap cleanup EXIT INT TERM

if [ -f "${hash_file}" ] && [ "$(cat "${hash_file}")" = "${PLUGIN_HASH}" ]; then
	echo "Plugins already up to date"
	exit 0
fi

cat > "${plugin_dir}/package.json" <<EOF
{"name":"n8n-plugin-bundle","private":true,"dependencies":${PLUGIN_DEPENDENCIES_JSON}}
EOF

cd "${plugin_dir}"
rm -rf node_modules package-lock.json
npm install --no-audit --no-fund --omit=dev
printf '%s' "${PLUGIN_HASH}" > "${hash_file}"
echo "Installed n8n plugins"
`

func (r *N8nInstanceReconciler) resolvePluginInstallPlan(
	ctx context.Context,
	instance *n8nv1alpha1.N8nInstance,
) (*pluginInstallPlan, error) {
	pluginList := &n8nv1alpha1.N8nPluginList{}
	if err := r.List(ctx, pluginList, client.InNamespace(instance.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list N8nPlugin resources: %w", err)
	}

	plugins := make([]n8nv1alpha1.N8nPlugin, 0, len(pluginList.Items))
	for i := range pluginList.Items {
		plugin := pluginList.Items[i]
		if strings.TrimSpace(plugin.Spec.InstanceRef.Name) != instance.Name {
			continue
		}
		plugins = append(plugins, plugin)
	}

	return buildPluginInstallPlan(plugins)
}

func (r *N8nInstanceReconciler) shouldUseSharedPluginCache(instance *n8nv1alpha1.N8nInstance) bool {
	if instance.Spec.Persistence == nil || len(instance.Spec.Persistence.AccessModes) == 0 {
		return false
	}
	for i := range instance.Spec.Persistence.AccessModes {
		if instance.Spec.Persistence.AccessModes[i] == corev1.ReadWriteMany {
			return true
		}
	}
	return false
}

func pluginPVCName(instance *n8nv1alpha1.N8nInstance) string {
	name := fmt.Sprintf("%s-plugins", instance.Name)
	if len(name) <= 63 {
		return name
	}

	base := instance.Name
	maxBaseLen := 63 - len("-plugins")
	if maxBaseLen <= 0 {
		return "plugins"
	}
	if len(base) > maxBaseLen {
		base = strings.TrimSuffix(base[:maxBaseLen], "-")
	}
	if base == "" {
		return "plugins"
	}
	return fmt.Sprintf("%s-plugins", base)
}

func (r *N8nInstanceReconciler) reconcilePluginPVC(ctx context.Context, instance *n8nv1alpha1.N8nInstance) error {
	logger := log.FromContext(ctx)
	pvcName := pluginPVCName(instance)

	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: instance.Namespace}, pvc)

	desired := r.buildPluginPVC(instance, pvcName)
	if err := controllerutil.SetControllerReference(instance, desired, r.Scheme); err != nil {
		return err
	}

	if errors.IsNotFound(err) {
		logger.Info("Creating plugin PVC", "name", pvcName)
		r.Recorder.Event(instance, corev1.EventTypeNormal, "PluginPVCCreated", fmt.Sprintf("Created plugin PVC %s", pvcName))
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	if !apiequality.Semantic.DeepEqual(pvc.Labels, desired.Labels) {
		pvc.Labels = desired.Labels
		logger.Info("Updating plugin PVC labels", "name", pvcName)
		return r.Update(ctx, pvc)
	}

	return nil
}

func (r *N8nInstanceReconciler) buildPluginPVC(
	instance *n8nv1alpha1.N8nInstance,
	name string,
) *corev1.PersistentVolumeClaim {
	accessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	if instance.Spec.Persistence != nil && len(instance.Spec.Persistence.AccessModes) > 0 {
		accessModes = instance.Spec.Persistence.AccessModes
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: instance.Namespace,
			Labels:    r.buildLabels(instance, "plugins"),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: accessModes,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}

	if instance.Spec.Persistence != nil && instance.Spec.Persistence.StorageClass != nil {
		pvc.Spec.StorageClassName = instance.Spec.Persistence.StorageClass
	}

	return pvc
}

func (r *N8nInstanceReconciler) buildPluginInstallerInitContainer(
	instance *n8nv1alpha1.N8nInstance,
	pluginPlan *pluginInstallPlan,
) corev1.Container {
	return corev1.Container{
		Name:            pluginInstallerName,
		Image:           instance.Spec.Image,
		ImagePullPolicy: instance.Spec.ImagePullPolicy,
		Command:         []string{"/bin/sh", "-c", pluginInstallerScript},
		Env: []corev1.EnvVar{
			{Name: "PLUGIN_HASH", Value: pluginPlan.Hash},
			{Name: "PLUGIN_DEPENDENCIES_JSON", Value: pluginPlan.DependenciesJSON},
		},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      pluginVolumeName,
			MountPath: pluginVolumeMountPath,
		}},
	}
}

func (r *N8nInstanceReconciler) mapPluginToInstance(_ context.Context, obj client.Object) []reconcile.Request {
	plugin, ok := obj.(*n8nv1alpha1.N8nPlugin)
	if !ok {
		return nil
	}

	instanceName := strings.TrimSpace(plugin.Spec.InstanceRef.Name)
	if instanceName == "" {
		return nil
	}

	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: plugin.Namespace,
			Name:      instanceName,
		},
	}}
}

func (r *N8nInstanceReconciler) setPluginStatusResolved(
	instance *n8nv1alpha1.N8nInstance,
	pluginPlan *pluginInstallPlan,
) {
	if pluginPlan == nil || !pluginPlan.Enabled {
		instance.Status.PluginCount = 0
		instance.Status.PluginHash = ""
		instance.Status.PluginPackages = nil
		apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
			Type:               pluginsConditionType,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: instance.Generation,
			Reason:             "NoPlugins",
			Message:            "No enabled plugins are configured",
		})
		return
	}

	instance.Status.PluginCount = int32(len(pluginPlan.PackageSpecs))
	instance.Status.PluginHash = pluginPlan.Hash
	instance.Status.PluginPackages = append([]string(nil), pluginPlan.PackageSpecs...)

	message := fmt.Sprintf("Resolved %d plugins with pod-local cache", len(pluginPlan.PackageSpecs))
	if pluginPlan.UseSharedCache {
		message = fmt.Sprintf("Resolved %d plugins with shared cache", len(pluginPlan.PackageSpecs))
	}

	apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               pluginsConditionType,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: instance.Generation,
		Reason:             "Resolved",
		Message:            message,
	})
}

func (r *N8nInstanceReconciler) setPluginStatusError(
	instance *n8nv1alpha1.N8nInstance,
	err error,
) {
	apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               pluginsConditionType,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: instance.Generation,
		Reason:             "ResolveFailed",
		Message:            fmt.Sprintf("Could not resolve plugins: %v", err),
	})
}
