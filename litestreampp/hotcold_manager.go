package litestreampp

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/benbjohnson/litestream"
)

// HotColdManager manages the lifecycle of hot and cold databases
type HotColdManager struct {
	mu sync.RWMutex

	// Core components
	store           *litestream.Store
	writeDetector   *WriteDetector
	sharedResources *SharedResourceManager
	connectionPool  *ConnectionPool

	// Configuration
	maxHotDBs       int
	scanInterval    time.Duration
	hotDuration     time.Duration
	replicaTemplate *ReplicaConfig // Template for creating replicas
	replicaFactory  ReplicaClientFactory // Factory for creating replica clients

	// Database tracking
	hotDatabases  map[string]*DynamicDB
	coldDatabases map[string]*ColdDBInfo
	hotReplicas   map[string]*litestream.Replica // Active replicas for hot databases

	// Metrics
	metrics *HierarchicalMetrics

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// ColdDBInfo tracks minimal info for cold databases
type ColdDBInfo struct {
	Path         string
	LastModTime  time.Time
	LastSize     int64
	Project      string
	Database     string
	Branch       string
	Tenant       string
}

// HotColdConfig contains configuration for the manager
type HotColdConfig struct {
	MaxHotDatabases int
	ScanInterval    time.Duration
	HotDuration     time.Duration
	Store           *litestream.Store
	SharedResources *SharedResourceManager
	ConnectionPool  *ConnectionPool
	ReplicaTemplate *ReplicaConfig // Template for creating replicas
	ReplicaFactory  ReplicaClientFactory // Factory for creating replica clients
}

// ReplicaClientFactory creates replica clients from configuration
type ReplicaClientFactory interface {
	CreateClient(config *ReplicaConfig, path string) (litestream.ReplicaClient, error)
}

// NewHotColdManager creates a new hot/cold manager
func NewHotColdManager(config *HotColdConfig) *HotColdManager {
	if config.ScanInterval == 0 {
		config.ScanInterval = 15 * time.Second
	}
	if config.HotDuration == 0 {
		config.HotDuration = 15 * time.Second
	}
	if config.MaxHotDatabases == 0 {
		config.MaxHotDatabases = 1000
	}

	mgr := &HotColdManager{
		store:           config.Store,
		sharedResources: config.SharedResources,
		connectionPool:  config.ConnectionPool,
		maxHotDBs:       config.MaxHotDatabases,
		scanInterval:    config.ScanInterval,
		hotDuration:     config.HotDuration,
		replicaTemplate: config.ReplicaTemplate,
		replicaFactory:  config.ReplicaFactory,
		hotDatabases:    make(map[string]*DynamicDB),
		coldDatabases:   make(map[string]*ColdDBInfo),
		hotReplicas:     make(map[string]*litestream.Replica),
		metrics:         GlobalMetrics,
	}

	// Create write detector
	mgr.writeDetector = NewWriteDetector(
		config.ScanInterval,
		config.HotDuration,
		config.MaxHotDatabases,
	)

	// Set callbacks for promotion/demotion
	mgr.writeDetector.SetCallbacks(
		mgr.promoteToHot,
		mgr.demoteToCold,
	)

	// Set shared resources
	mgr.writeDetector.SetResources(config.SharedResources, config.ConnectionPool)

	return mgr
}

// Start begins managing databases
func (m *HotColdManager) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)

	// Start write detector
	m.writeDetector.Start(m.ctx)

	// Start management loop
	m.wg.Add(1)
	go m.managementLoop()

	slog.Info("hot/cold manager started",
		"max_hot_dbs", m.maxHotDBs,
		"scan_interval", m.scanInterval,
		"hot_duration", m.hotDuration)

	return nil
}

// Stop stops the manager
func (m *HotColdManager) Stop() error {
	if m.cancel != nil {
		m.cancel()
	}

	// Stop write detector
	m.writeDetector.Stop()

	// Wait for management loop
	m.wg.Wait()

	// Close all hot databases and stop replicas
	m.mu.Lock()
	defer m.mu.Unlock()

	for path, db := range m.hotDatabases {
		// Stop replica if exists
		if replica, ok := m.hotReplicas[path]; ok {
			if err := replica.Stop(true); err != nil {
				slog.Error("failed to stop replica", "path", path, "error", err)
			}
			delete(m.hotReplicas, path)
		}
		
		if err := db.Close(context.Background()); err != nil {
			slog.Error("failed to close hot database", "path", path, "error", err)
		}
	}

	slog.Info("hot/cold manager stopped")
	return nil
}

// managementLoop handles periodic management tasks
func (m *HotColdManager) managementLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.updateMetrics()
			m.logStatistics()
		}
	}
}

// promoteToHot promotes a database to hot tier
func (m *HotColdManager) promoteToHot(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already hot
	if _, ok := m.hotDatabases[path]; ok {
		return nil
	}

	// Remove from cold if present
	delete(m.coldDatabases, path)

	// Create dynamic DB
	db := litestream.NewDB(path)
	dynamicDB := &DynamicDB{
		DB:       db,
		state:    DBStateClosed,
		manager:  nil, // Not using MultiDBManager for now
		lastAccess: time.Now(),
	}

	// Set callbacks for lifecycle events
	dynamicDB.onOpen = func(d *DynamicDB) error {
		// TODO: Add to store when opened
		// The current Store doesn't support dynamic addition of DBs
		// if m.store != nil {
		//     m.store.AddDB(d.DB)
		// }
		
		// Submit monitoring task to worker pool
		if m.sharedResources != nil {
			m.sharedResources.monitorPool.Submit(&MonitorTask{
				Path:     path,
				Interval: 1 * time.Second,
				DB:       dynamicDB,
			})
		}

		slog.Debug("database promoted to hot", "path", path)
		return nil
	}

	dynamicDB.onClose = func(d *DynamicDB) error {
		// TODO: Remove from store when closed
		// The current Store doesn't support dynamic removal of DBs
		// if m.store != nil {
		//     m.store.RemoveDB(d.DB)
		// }
		
		slog.Debug("database closed", "path", path)
		return nil
	}

	// Open the database
	if err := dynamicDB.Open(context.Background()); err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	// Create and start replica if configured
	if m.replicaTemplate != nil {
		replica, err := m.createReplicaForDB(dynamicDB.DB, path)
		if err != nil {
			slog.Error("failed to create replica", "path", path, "error", err)
			// Continue without replication rather than failing promotion
		} else if replica != nil {
			// Assign replica to database
			dynamicDB.DB.Replica = replica
			
			// Start replica monitoring
			if err := replica.Start(m.ctx); err != nil {
				slog.Error("failed to start replica", "path", path, "error", err)
			} else {
				m.hotReplicas[path] = replica
				slog.Debug("replica started", "path", path, "type", m.replicaTemplate.Type)
			}
		}
	}

	m.hotDatabases[path] = dynamicDB

	// Update metrics
	if m.metrics != nil {
		project, database, _, _ := ParseDBPath(path)
		m.metrics.UpdateDatabaseStats(project, database, 1, 1, 1)
	}

	slog.Info("database promoted to hot tier", "path", filepath.Base(path))
	return nil
}

// demoteToCold demotes a database to cold tier
func (m *HotColdManager) demoteToCold(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get hot database
	db, ok := m.hotDatabases[path]
	if !ok {
		return nil // Not hot
	}

	// Stop replica if exists
	if replica, ok := m.hotReplicas[path]; ok {
		// Perform final sync before stopping
		if err := replica.Sync(context.Background()); err != nil {
			slog.Debug("final sync before demotion failed", "path", path, "error", err)
		}
		
		if err := replica.Stop(false); err != nil {
			slog.Error("failed to stop replica during demotion", "path", path, "error", err)
		}
		delete(m.hotReplicas, path)
		
		// Clear replica from database
		db.DB.Replica = nil
	}
	
	// Close the database
	if err := db.Close(context.Background()); err != nil {
		slog.Error("failed to close database during demotion", "path", path, "error", err)
	}

	// Remove from hot
	delete(m.hotDatabases, path)

	// Add to cold
	project, database, branch, tenant := ParseDBPath(path)
	m.coldDatabases[path] = &ColdDBInfo{
		Path:     path,
		Project:  project,
		Database: database,
		Branch:   branch,
		Tenant:   tenant,
	}

	// Update metrics
	if m.metrics != nil {
		m.metrics.UpdateDatabaseStats(project, database, 1, 1, 0)
	}

	slog.Info("database demoted to cold tier", "path", filepath.Base(path))
	return nil
}

// AddDatabases adds databases to manage from glob patterns
func (m *HotColdManager) AddDatabases(patterns []string) error {
	// Add to write detector
	if err := m.writeDetector.AddDatabases(patterns); err != nil {
		return fmt.Errorf("add databases to write detector: %w", err)
	}

	// Track all databases as cold initially
	m.mu.Lock()
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			slog.Error("glob pattern failed", "pattern", pattern, "error", err)
			continue
		}

		for _, path := range matches {
			if _, hotOk := m.hotDatabases[path]; hotOk {
				continue // Already hot
			}
			if _, coldOk := m.coldDatabases[path]; coldOk {
				continue // Already cold
			}

			// Add as cold
			project, database, branch, tenant := ParseDBPath(path)
			m.coldDatabases[path] = &ColdDBInfo{
				Path:     path,
				Project:  project,
				Database: database,
				Branch:   branch,
				Tenant:   tenant,
			}
		}
	}
	m.mu.Unlock()

	// Update metrics after releasing lock
	m.updateMetrics()
	return nil
}

// updateMetrics updates hierarchical metrics
func (m *HotColdManager) updateMetrics() {
	if m.metrics == nil {
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Update tier counts
	m.metrics.UpdateTierCounts(len(m.hotDatabases), len(m.coldDatabases))

	// Aggregate by project
	projectStats := make(map[string]struct {
		total int
		hot   int
	})

	for _, cold := range m.coldDatabases {
		key := cold.Project
		stats := projectStats[key]
		stats.total++
		projectStats[key] = stats
	}

	for path := range m.hotDatabases {
		project, _, _, _ := ParseDBPath(path)
		stats := projectStats[project]
		stats.total++
		stats.hot++
		projectStats[project] = stats
	}

	// Update project metrics
	for project, stats := range projectStats {
		m.metrics.UpdateProjectStats(project, stats.total, stats.hot)
	}
}

// logStatistics logs current statistics
func (m *HotColdManager) logStatistics() {
	m.mu.RLock()
	hotCount := len(m.hotDatabases)
	coldCount := len(m.coldDatabases)
	m.mu.RUnlock()

	total, detectorHot, _ := m.writeDetector.GetStatistics()

	slog.Info("hot/cold manager statistics",
		"total_tracked", total,
		"hot_databases", hotCount,
		"cold_databases", coldCount,
		"detector_hot", detectorHot)
}

// GetStatistics returns current statistics
func (m *HotColdManager) GetStatistics() (total, hot, cold int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	hot = len(m.hotDatabases)
	cold = len(m.coldDatabases)
	total = hot + cold
	return
}

// GetHotDatabases returns list of hot database paths
func (m *HotColdManager) GetHotDatabases() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	paths := make([]string, 0, len(m.hotDatabases))
	for path := range m.hotDatabases {
		paths = append(paths, path)
	}
	return paths
}

// IsHot checks if a database is hot
func (m *HotColdManager) IsHot(path string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.hotDatabases[path]
	return ok
}

// createReplicaForDB creates a replica for a database based on the template
func (m *HotColdManager) createReplicaForDB(db *litestream.DB, path string) (*litestream.Replica, error) {
	if m.replicaTemplate == nil || m.replicaFactory == nil {
		return nil, nil // No replication configured
	}
	
	// Expand path template
	expandedPath := m.expandPathTemplate(m.replicaTemplate.Path, path)
	
	// Create a copy of the config with expanded path
	config := *m.replicaTemplate
	config.Path = expandedPath
	
	// Use factory to create client
	client, err := m.replicaFactory.CreateClient(&config, path)
	if err != nil {
		return nil, fmt.Errorf("create replica client: %w", err)
	}
	
	if client == nil {
		return nil, nil // Factory returned nil client
	}
	
	// Create replica with client
	replica := litestream.NewReplicaWithClient(db, client)
	
	// Apply configuration from template
	if m.replicaTemplate.SyncInterval > 0 {
		replica.SyncInterval = m.replicaTemplate.SyncInterval
	}
	
	return replica, nil
}

// expandPathTemplate expands template variables in the path
func (m *HotColdManager) expandPathTemplate(template, dbPath string) string {
	if template == "" {
		return ""
	}
	
	// Parse database path components
	project, database, branch, tenant := ParseDBPath(dbPath)
	
	// Replace template variables
	result := template
	result = strings.ReplaceAll(result, "{{project}}", project)
	result = strings.ReplaceAll(result, "{{database}}", database)
	result = strings.ReplaceAll(result, "{{branch}}", branch)
	result = strings.ReplaceAll(result, "{{tenant}}", tenant)
	
	// Also support filename without extension
	filename := filepath.Base(dbPath)
	if ext := filepath.Ext(filename); ext != "" {
		filename = filename[:len(filename)-len(ext)]
	}
	result = strings.ReplaceAll(result, "{{filename}}", filename)
	
	return result
}