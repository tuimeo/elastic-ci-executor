package executor

import (
	"fmt"
	"os"
)

// GetJobID retrieves the job ID from environment variables.
// In production, it reads from CUSTOM_ENV_CI_JOB_ID set by GitLab Runner.
// For local debugging, ECIE_DEBUG_JOB_ID can be used to override.
func GetJobID() (string, error) {
	// Allow explicit override for local debugging/testing
	if override := os.Getenv("ECIE_DEBUG_JOB_ID"); override != "" {
		return override, nil
	}
	// Production: GitLab Runner Custom Executor passes variables with CUSTOM_ENV_ prefix
	jobID := os.Getenv("CUSTOM_ENV_CI_JOB_ID")
	if jobID == "" {
		return "", fmt.Errorf("CUSTOM_ENV_CI_JOB_ID not set: this executor must run under GitLab Runner; set ECIE_DEBUG_JOB_ID for local testing")
	}
	return jobID, nil
}
