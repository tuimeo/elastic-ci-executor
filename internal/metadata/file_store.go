package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileStore implements Store interface using flat JSON files.
// Each job is stored as <job-id>.json in the base directory.
type FileStore struct {
	baseDir string
}

// NewFileStore creates a new file-based metadata store.
// The baseDir is created if it doesn't exist.
func NewFileStore(baseDir string) (*FileStore, error) {
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create jobstore directory %s: %w", baseDir, err)
	}
	return &FileStore{baseDir: baseDir}, nil
}

// Save persists container metadata to a JSON file.
func (s *FileStore) Save(jobID string, data Job) error {
	data.JobID = jobID
	data.CreatedAt = time.Now()

	filename := s.jobPath(jobID)
	file, err := os.CreateTemp(filepath.Dir(filename), "*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := file.Name()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to encode metadata: %w", err)
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, filename); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to save metadata: %w", err)
	}

	return nil
}

// Load retrieves container metadata from a JSON file.
func (s *FileStore) Load(jobID string) (Job, error) {
	filename := s.jobPath(jobID)
	data, err := os.ReadFile(filename) //nolint:gosec // G304 - path derived from trusted job ID
	if err != nil {
		if os.IsNotExist(err) {
			return Job{}, fmt.Errorf("metadata not found for job %s", jobID)
		}
		return Job{}, fmt.Errorf("failed to read metadata: %w", err)
	}

	var meta Job
	if err := json.Unmarshal(data, &meta); err != nil {
		return Job{}, fmt.Errorf("failed to decode metadata: %w", err)
	}

	return meta, nil
}

// Delete removes container metadata file.
func (s *FileStore) Delete(jobID string) error {
	filename := s.jobPath(jobID)
	if err := os.Remove(filename); err != nil {
		if os.IsNotExist(err) {
			return nil // Already deleted, not an error
		}
		return fmt.Errorf("failed to delete metadata: %w", err)
	}
	return nil
}

// List returns all stored container metadata.
func (s *FileStore) List() ([]Job, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read jobstore directory: %w", err)
	}

	var jobs []Job
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}

		jobID := strings.TrimSuffix(name, ".json")
		meta, err := s.Load(jobID)
		if err != nil {
			// Skip corrupted files
			continue
		}
		jobs = append(jobs, meta)
	}

	return jobs, nil
}

// jobPath returns the full path for a job's metadata file.
func (s *FileStore) jobPath(jobID string) string {
	return filepath.Join(s.baseDir, jobID+".json")
}

// Close is a no-op for FileStore.
func (s *FileStore) Close() error {
	return nil
}
