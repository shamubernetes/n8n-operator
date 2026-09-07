package controller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	n8nv1alpha1 "github.com/shamubernetes/n8n-operator/api/v1alpha1"
)

// Exercise the actual init-container shell script without network or Kubernetes.
func TestPluginInstallerScript(t *testing.T) {
	for _, cache := range []string{"cold", "legacy"} {
		t.Run(cache, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			plugins := filepath.Join(dir, "nodes")
			for _, path := range []string{bin, plugins} {
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, content string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(bin, "node"), "#!/bin/sh\nprintf '%s' \"${TEST_RUNTIME}\"\n")
			write(filepath.Join(bin, "npm"), `#!/bin/sh
set -eu
if [ "$*" != 'install --no-audit --no-fund --omit=dev --legacy-peer-deps' ]; then
  echo "Unexpected install policy: $*" >&2
  exit 42
fi
if [ "${TEST_FAIL}" = 1 ]; then exit 43; fi
printf 'installed\n' >> "${TEST_CALLS}"
mkdir -p node_modules
`)
			hashFile := filepath.Join(plugins, ".plugin-hash")
			if cache == "legacy" {
				write(hashFile, "dependencies-v1")
			}
			lockCase := ""
			write(filepath.Join(bin, "mkdir"), `#!/bin/sh
set -eu
if [ "$*" = "${TEST_PLUGIN_DIR}/.install-lock" ] && [ -n "${TEST_LOCK_CASE}" ]; then
  cp "${TEST_COMPLETED_HASH}" "${TEST_PLUGIN_DIR}/.plugin-hash"
  if [ "${TEST_LOCK_CASE}" = waiting ]; then exit 1; fi
fi
exec /bin/mkdir "$@"
`)
			run := func(hash, image, runtime string, fail bool) error {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "/bin/sh", "-c", strings.ReplaceAll(pluginInstallerScript, pluginVolumeMountPath, plugins))
				failure := "0"
				if fail {
					failure = "1"
				}
				cmd.Env = append(os.Environ(),
					"PATH="+bin+":"+os.Getenv("PATH"),
					"PLUGIN_HASH="+hash, "PLUGIN_IMAGE="+image,
					`PLUGIN_DEPENDENCIES_JSON={"n8n-nodes-test":"1.0.0"}`,
					"TEST_RUNTIME="+runtime, "TEST_FAIL="+failure,
					"TEST_LOCK_CASE="+lockCase, "TEST_PLUGIN_DIR="+plugins,
					"TEST_COMPLETED_HASH="+filepath.Join(dir, "completed-hash"),
					"TEST_CALLS="+filepath.Join(dir, "calls"))
				// Avoid waiting for inherited pipes in the script's heartbeat process.
				outputFile, err := os.Create(filepath.Join(dir, "output"))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = outputFile.Close() }()
				cmd.Stdout, cmd.Stderr = outputFile, outputFile
				err = cmd.Run()
				if err != nil {
					output, _ := os.ReadFile(outputFile.Name())
					t.Logf("installer: %s", output)
				}
				return err
			}
			calls := func(want int) {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(dir, "calls"))
				if err != nil {
					t.Fatal(err)
				}
				if got := strings.Count(string(data), "installed\n"); got != want {
					t.Fatalf("npm calls = %d, want %d", got, want)
				}
			}
			for i, input := range []struct{ hash, image, runtime string }{
				{"dependencies-v1", "n8n:old", "linux:x64:141"},
				{"dependencies-v2", "n8n:old", "linux:x64:141"},
				{"dependencies-v2", "n8n:new", "linux:x64:141"},
				{"dependencies-v2", "n8n:new", "linux:x64:148"},
				{"dependencies-v2", "n8n:new", "linux:arm64:148"},
			} {
				if err := run(input.hash, input.image, input.runtime, false); err != nil {
					t.Fatalf("cold/invalidated installation failed: %v", err)
				}
				calls(i + 1)
				if err := run(input.hash, input.image, input.runtime, true); err != nil {
					t.Fatalf("matching cache should skip npm: %v", err)
				}
				calls(i + 1)
			}
			before, err := os.ReadFile(hashFile)
			if err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(dir, "completed-hash"), string(before))
			for _, state := range []string{"waiting", "acquired"} {
				lockCase = state
				if err := os.Remove(hashFile); err != nil {
					t.Fatal(err)
				}
				if err := run("dependencies-v2", "n8n:new", "linux:arm64:148", true); err != nil {
					t.Fatalf("cache completed while lock %s should skip npm: %v", state, err)
				}
				calls(5)
			}
			lockCase = ""
			if err := run("failed-dependencies", "n8n:new", "linux:arm64:148", true); err == nil {
				t.Fatal("expected npm failure to fail the installer")
			}
			if _, err := os.Stat(hashFile); !os.IsNotExist(err) {
				t.Fatalf("failed replacement must leave no success marker: %v", err)
			}
			if _, err := os.Stat(filepath.Join(plugins, ".install-lock")); !os.IsNotExist(err) {
				t.Fatalf("installation lock was not removed: %v", err)
			}
			if err := run("dependencies-v2", "n8n:new", "linux:arm64:148", false); err != nil {
				t.Fatalf("rollback installation failed: %v", err)
			}
			calls(6)
		})
	}
}

func TestPluginInstallerImageCacheIdentity(t *testing.T) {
	instance := &n8nv1alpha1.N8nInstance{Spec: n8nv1alpha1.N8nInstanceSpec{Image: "n8n:2.38.4@sha256:test"}}
	r := &N8nInstanceReconciler{}
	container := r.buildPluginInstallerInitContainer(instance, &pluginInstallPlan{Hash: "test"})
	for _, env := range container.Env {
		if env.Name == "PLUGIN_IMAGE" && env.Value == container.Image {
			return
		}
	}
	t.Fatal("installer cache must be bound to the exact n8n image reference")
}
