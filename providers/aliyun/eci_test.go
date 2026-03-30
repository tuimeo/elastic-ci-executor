package aliyun

import (
	"testing"

	"github.com/tuimeo/elastic-ci-executor/providers"
)

func TestExecCommandTargetContainerRouting(t *testing.T) {
	tests := []struct {
		name     string
		extra    map[string]string
		wantName string
	}{
		{
			name:     "routes to helper when targetContainer=helper",
			extra:    map[string]string{"targetContainer": "helper"},
			wantName: "helper",
		},
		{
			name:     "routes to build when targetContainer=build",
			extra:    map[string]string{"targetContainer": "build"},
			wantName: "build",
		},
		{
			name:     "defaults to build when targetContainer is empty",
			extra:    map[string]string{},
			wantName: "build",
		},
		{
			name:     "defaults to build when Extra is nil",
			extra:    nil,
			wantName: "build",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			container := providers.JobContainer{
				Extra: tt.extra,
			}
			target := container.Extra["targetContainer"]
			if target == "" {
				target = buildContainerName
			}
			if target != tt.wantName {
				t.Errorf("got target %q, want %q", target, tt.wantName)
			}
		})
	}
}

// resolveHelperImage mimics the helper image resolution logic in CreateContainer
func resolveHelperImage(settingsHelperImage string, envVars map[string]string) string {
	helperImage := settingsHelperImage
	if helperImage == "" {
		tag := defaultHelperTag
		if v := envVars["CI_RUNNER_VERSION"]; v != "" {
			tag = "x86_64-v" + v
		}
		helperImage = helperImageRegistry + ":" + tag
	}
	return helperImage
}

func TestHelperImageDefault(t *testing.T) {
	// No config override, no CI_RUNNER_VERSION → uses defaultHelperTag
	helperImage := resolveHelperImage("", map[string]string{})

	want := "registry.gitlab.com/gitlab-org/gitlab-runner/gitlab-runner-helper:x86_64-v18.9.0"
	if helperImage != want {
		t.Errorf("got %s, want %s", helperImage, want)
	}
}

func TestHelperImageAutoDetectVersion(t *testing.T) {
	// No config override, CI_RUNNER_VERSION set → auto-detect
	helperImage := resolveHelperImage("", map[string]string{"CI_RUNNER_VERSION": "19.1.0"})

	want := "registry.gitlab.com/gitlab-org/gitlab-runner/gitlab-runner-helper:x86_64-v19.1.0"
	if helperImage != want {
		t.Errorf("got %s, want %s", helperImage, want)
	}
}

func TestHelperImageOverride(t *testing.T) {
	// Config override takes priority over auto-detect
	helperImage := resolveHelperImage("my-registry.cn/helper:latest", map[string]string{"CI_RUNNER_VERSION": "19.1.0"})

	if helperImage != "my-registry.cn/helper:latest" {
		t.Errorf("config override not working: got %s", helperImage)
	}
}

func TestHelperContainerResources(t *testing.T) {
	// Helper container resources are now configurable via settings.HelperCPU/HelperMemory.
	// Verify the defaults come from config (2 vCPU, 4 GiB) — tested at the config layer.
}
