package litestreampp

import (
	"time"
)

// MultiDBConfig represents multi-database configuration
type MultiDBConfig struct {
	Enabled          bool                  `yaml:"enabled"`
	Patterns         []string              `yaml:"patterns"`
	MaxHotDatabases  int                   `yaml:"max-hot-databases"`
	ScanInterval     time.Duration         `yaml:"scan-interval"`
	ReplicaTemplate  *ReplicaConfig        `yaml:"replica-template"`
	ColdSyncInterval time.Duration         `yaml:"cold-sync-interval"`
	ColdSyncMode     string                `yaml:"cold-sync-mode"`
	HotPromotion     HotPromotionConfig    `yaml:"hot-promotion"`
}

// HotPromotionConfig defines criteria for promoting databases to hot tier
type HotPromotionConfig struct {
	RecentModifyThreshold time.Duration `yaml:"recent-modify-threshold"`
	AccessCountThreshold  int64         `yaml:"access-count-threshold"`
}

// ReplicaConfig represents configuration for a replica
// This would match the existing replica config structure
type ReplicaConfig struct {
	Type         string         `yaml:"type"`
	Name         string         `yaml:"name"`
	Path         string         `yaml:"path"`
	URL          string         `yaml:"url"`
	Bucket       string         `yaml:"bucket"`
	Region       string         `yaml:"region"`
	Endpoint     string         `yaml:"endpoint"`
	SyncInterval time.Duration  `yaml:"sync-interval"`
	
	// S3 specific
	AccessKeyID     string `yaml:"access-key-id"`
	SecretAccessKey string `yaml:"secret-access-key"`
}

// DefaultMultiDBConfig returns default multi-database configuration
func DefaultMultiDBConfig() *MultiDBConfig {
	return &MultiDBConfig{
		Enabled:          false,
		MaxHotDatabases:  1000,
		ScanInterval:     30 * time.Second,
		ColdSyncInterval: 30 * time.Second,
		ColdSyncMode:     "snapshot",
		HotPromotion: HotPromotionConfig{
			RecentModifyThreshold: 5 * time.Minute,
			AccessCountThreshold:  10,
		},
	}
}