package job

// Writer is the interface for modifying jobs.
type Writer interface {
	UploadAsset(jobID string, fileName string, contentType string, content []byte) error
}