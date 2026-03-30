package metadata

import "time"

// Job contains information about a running container instance
type Job struct {
	JobID       string            `json:"job_id"`
	Provider    string            `json:"provider"`
	ContainerID string            `json:"container_id"`
	Region      string            `json:"region"`
	CreatedAt   time.Time         `json:"created_at"`
	Extra       map[string]string `json:"extra,omitempty"`
}
