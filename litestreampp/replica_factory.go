package litestreampp

import (
	"fmt"

	"github.com/benbjohnson/litestream"
)

// DefaultReplicaClientFactory is the default implementation of ReplicaClientFactory
// Note: The actual S3 client creation is done in the cmd package to avoid import cycles
type DefaultReplicaClientFactory struct {
	// CreateS3ClientFunc is injected to avoid import cycles
	CreateS3ClientFunc func(config *ReplicaConfig) (litestream.ReplicaClient, error)
}

// NewDefaultReplicaClientFactory creates a new default replica client factory
func NewDefaultReplicaClientFactory() *DefaultReplicaClientFactory {
	return &DefaultReplicaClientFactory{}
}

// CreateClient creates a replica client based on configuration
func (f *DefaultReplicaClientFactory) CreateClient(config *ReplicaConfig, dbPath string) (litestream.ReplicaClient, error) {
	if config == nil {
		return nil, nil
	}
	
	switch config.Type {
	case "s3":
		if f.CreateS3ClientFunc != nil {
			return f.CreateS3ClientFunc(config)
		}
		return nil, fmt.Errorf("S3 client factory not configured")
		
	case "file":
		// File-based replication for testing
		// TODO: Implement file replica client
		return nil, nil
		
	default:
		return nil, fmt.Errorf("unsupported replica type: %s", config.Type)
	}
}

// CreateS3ReplicaClient is a helper that will be injected from cmd package
// This avoids import cycles while keeping the factory pattern
func CreateS3ReplicaClient(config *ReplicaConfig) (litestream.ReplicaClient, error) {
	// This will be implemented in cmd package where we can import s3
	return nil, fmt.Errorf("S3 replica client creation must be injected")
}