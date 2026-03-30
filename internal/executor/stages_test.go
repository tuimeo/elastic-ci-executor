package executor

import "testing"

func TestTargetContainerForStage(t *testing.T) {
	helperCases := []string{
		"prepare_script",
		"get_sources",
		"clear_worktree",
		"restore_cache",
		"download_artifacts",
		"archive_cache",
		"archive_cache_on_failure",
		"upload_artifacts_on_success",
		"upload_artifacts_on_failure",
		"cleanup_file_variables",
	}
	for _, stage := range helperCases {
		if got := targetContainerForStage(stage); got != "helper" {
			t.Errorf("targetContainerForStage(%q) = %q, want %q", stage, got, "helper")
		}
	}

	buildCases := []string{
		"build_script",
		"step_script",
		"step_release",
		"after_script",
		"some_unknown_stage",
	}
	for _, stage := range buildCases {
		if got := targetContainerForStage(stage); got != "build" {
			t.Errorf("targetContainerForStage(%q) = %q, want %q", stage, got, "build")
		}
	}
}
