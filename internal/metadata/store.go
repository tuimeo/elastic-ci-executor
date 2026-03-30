package metadata

// Store provides an interface for persisting and retrieving container metadata
type Store interface {
	// Save persists container metadata for a given job ID
	Save(jobID string, data Job) error

	// Load retrieves container metadata for a given job ID
	Load(jobID string) (Job, error)

	// Delete removes container metadata for a given job ID
	Delete(jobID string) error

	// List returns all stored container metadata
	List() ([]Job, error)
}
