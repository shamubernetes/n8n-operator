package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

const (
	pluginReadyConditionType = "Ready"

	pluginHashAnnotation  = "n8n.n8n.io/plugin-hash"
	pluginVolumeName      = "plugins"
	pluginVolumeMountPath = "/home/node/.n8n/nodes"
	pluginInstallerName   = "plugin-installer"
)

type pluginInstallPlan struct {
	Enabled          bool
	DependenciesJSON string
	Hash             string
	PackageSpecs     []string
	UseSharedCache   bool
}

func resolvePluginVersion(version string) string {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return "latest"
	}
	return trimmed
}

func resolvePluginPackageSpec(spec n8nv1alpha1.N8nPluginSpec) string {
	version := resolvePluginVersion(spec.Version)
	return fmt.Sprintf("%s@%s", strings.TrimSpace(spec.PackageName), version)
}

func buildPluginInstallPlan(plugins []n8nv1alpha1.N8nPlugin) (*pluginInstallPlan, error) {
	deps := make(map[string]string)
	names := make(map[string]string)

	for i := range plugins {
		plugin := &plugins[i]
		if plugin.Spec.Enabled != nil && !*plugin.Spec.Enabled {
			continue
		}

		pkg := strings.TrimSpace(plugin.Spec.PackageName)
		if pkg == "" {
			continue
		}

		version := resolvePluginVersion(plugin.Spec.Version)
		if existing, exists := deps[pkg]; exists && existing != version {
			return nil, fmt.Errorf(
				"conflicting plugin versions for package %s: %s (%s) vs %s (%s)",
				pkg,
				existing,
				names[pkg],
				version,
				plugin.Name,
			)
		}

		deps[pkg] = version
		names[pkg] = plugin.Name
	}

	if len(deps) == 0 {
		return &pluginInstallPlan{}, nil
	}

	depsJSONBytes, err := json.Marshal(deps)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal plugin dependencies: %w", err)
	}

	hashRaw := sha256.Sum256(depsJSONBytes)
	packageSpecs := make([]string, 0, len(deps))
	for pkg, version := range deps {
		packageSpecs = append(packageSpecs, fmt.Sprintf("%s@%s", pkg, version))
	}
	sort.Strings(packageSpecs)

	return &pluginInstallPlan{
		Enabled:          true,
		DependenciesJSON: string(depsJSONBytes),
		Hash:             hex.EncodeToString(hashRaw[:]),
		PackageSpecs:     packageSpecs,
	}, nil
}
