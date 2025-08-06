package litestreampp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/benbjohnson/litestream"
)

// IntegratedMultiDBManager combines MultiDBManager with HotColdManager for Phase 3
type IntegratedMultiDBManager struct {
	mu sync.RWMutex

	// Core components
	store           *litestream.Store
	hotColdManager  *HotColdManager
	sharedResources *SharedResourceManager
	connectionPool  *ConnectionPool

	// Configuration
	config *MultiDBConfig

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewIntegratedMultiDBManager creates a new integrated manager
func NewIntegratedMultiDBManager(store *litestream.Store, config *MultiDBConfig) (*IntegratedMultiDBManager, error) {
	// Create shared resources
	sharedResources := NewSharedResourceManager()
	
	// Create connection pool
	connectionPool := NewConnectionPool(config.MaxHotDatabases, 5*time.Second)
	
	// Create replica factory if replication is configured
	var replicaFactory ReplicaClientFactory
	if config.ReplicaTemplate != nil {
		factory := NewDefaultReplicaClientFactory()
		// Note: S3 client creation function must be injected from cmd package
		// to avoid import cycles
		replicaFactory = factory
	}
	
	// Create hot/cold configuration
	hotColdConfig := &HotColdConfig{
		MaxHotDatabases: config.MaxHotDatabases,
		ScanInterval:    config.ScanInterval,
		HotDuration:     config.HotPromotion.RecentModifyThreshold,
		Store:           store,
		SharedResources: sharedResources,
		ConnectionPool:  connectionPool,
		ReplicaTemplate: config.ReplicaTemplate, // Pass replica template
		ReplicaFactory:  replicaFactory,
	}
	
	// Create hot/cold manager
	hotColdManager := NewHotColdManager(hotColdConfig)
	
	return &IntegratedMultiDBManager{
		store:           store,
		hotColdManager:  hotColdManager,
		sharedResources: sharedResources,
		connectionPool:  connectionPool,
		config:          config,
	}, nil
}

// Start begins managing databases
func (m *IntegratedMultiDBManager) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)
	
	// Start connection pool cleanup
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.connectionPool.Start(m.ctx)
	}()
	
	// Start hot/cold manager
	if err := m.hotColdManager.Start(m.ctx); err != nil {
		return fmt.Errorf("start hot/cold manager: %w", err)
	}
	
	// Add databases from patterns
	if err := m.hotColdManager.AddDatabases(m.config.Patterns); err != nil {
		return fmt.Errorf("add databases: %w", err)
	}
	
	// Start monitoring
	m.wg.Add(1)
	go m.monitorLoop()
	
	slog.Info("integrated multi-DB manager started",
		"patterns", m.config.Patterns,
		"max_hot_databases", m.config.MaxHotDatabases,
		"scan_interval", m.config.ScanInterval)
	
	return nil
}

// Stop stops the manager
func (m *IntegratedMultiDBManager) Stop() error {
	if m.cancel != nil {
		m.cancel()
	}
	
	// Stop hot/cold manager
	if err := m.hotColdManager.Stop(); err != nil {
		slog.Error("failed to stop hot/cold manager", "error", err)
	}
	
	// Wait for goroutines
	m.wg.Wait()
	
	slog.Info("integrated multi-DB manager stopped")
	return nil
}

// monitorLoop monitors system health and logs statistics
func (m *IntegratedMultiDBManager) monitorLoop() {
	defer m.wg.Done()
	
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.logStatistics()
		}
	}
}

// logStatistics logs current system statistics
func (m *IntegratedMultiDBManager) logStatistics() {
	total, hot, cold := m.hotColdManager.GetStatistics()
	connStats := m.connectionPool.Stats()
	
	slog.Info("system statistics",
		"total_databases", total,
		"hot_databases", hot,
		"cold_databases", cold,
		"open_connections", connStats.CurrentOpen,
		"total_connections", connStats.TotalOpened)
	
	// Log shared resource stats (if we add ActiveCount method later)
	// Currently worker pools don't expose active count
}


// SetS3ClientFactory sets the S3 client creation function
// This must be called from the cmd package to avoid import cycles
func (m *IntegratedMultiDBManager) SetS3ClientFactory(fn func(*ReplicaConfig) (litestream.ReplicaClient, error)) {
	if m.hotColdManager != nil && m.hotColdManager.replicaFactory != nil {
		if factory, ok := m.hotColdManager.replicaFactory.(*DefaultReplicaClientFactory); ok {
			factory.CreateS3ClientFunc = fn
		}
	}
}

// GetStatistics returns current statistics
func (m *IntegratedMultiDBManager) GetStatistics() (total, hot, cold int, connStats ConnectionPoolStats) {
	total, hot, cold = m.hotColdManager.GetStatistics()
	connStats = m.connectionPool.Stats()
	return
}

// GetHotDatabases returns list of hot database paths
func (m *IntegratedMultiDBManager) GetHotDatabases() []string {
	return m.hotColdManager.GetHotDatabases()
}

// IsHot checks if a database is hot
func (m *IntegratedMultiDBManager) IsHot(path string) bool {
	return m.hotColdManager.IsHot(path)
}

// RefreshPatterns re-scans the patterns for new databases
func (m *IntegratedMultiDBManager) RefreshPatterns() error {
	return m.hotColdManager.AddDatabases(m.config.Patterns)
}