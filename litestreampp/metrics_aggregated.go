package litestreampp

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// GlobalMetrics is the global aggregated metrics instance
var GlobalMetrics *HierarchicalMetrics

func init() {
	GlobalMetrics = NewHierarchicalMetrics()
}

// HierarchicalMetrics provides aggregated metrics at multiple levels
type HierarchicalMetrics struct {
	mu sync.RWMutex

	// System-wide metrics (no labels)
	totalHotDBs    prometheus.Gauge
	totalColdDBs   prometheus.Gauge
	totalDBSize    prometheus.Gauge
	totalWALSize   prometheus.Gauge
	totalWALBytes  prometheus.Counter

	// Project-level metrics (label: project)
	projectDBCount      *prometheus.GaugeVec
	projectDBSize       *prometheus.GaugeVec
	projectActiveDBs    *prometheus.GaugeVec
	projectSyncOps      *prometheus.CounterVec
	projectSyncDuration *prometheus.HistogramVec

	// Database-level metrics (labels: project, database)
	databaseTenantCount *prometheus.GaugeVec
	databaseBranchCount *prometheus.GaugeVec
	databaseHotTenants  *prometheus.GaugeVec
	databaseSize        *prometheus.GaugeVec

	// Tier-based metrics (label: tier = "hot" or "cold")
	tierSyncOps      *prometheus.CounterVec
	tierSyncDuration *prometheus.HistogramVec
	tierSyncErrors   *prometheus.CounterVec
	tierWALBytes     *prometheus.CounterVec

	// Internal tracking
	projectStats  map[string]*ProjectStats
	databaseStats map[string]*DatabaseStats
}

// ProjectStats tracks statistics for a project
type ProjectStats struct {
	TotalDBs     int
	ActiveDBs    int
	TotalSize    int64
	TotalWALSize int64
	LastUpdated  time.Time
}

// DatabaseStats tracks statistics for a database
type DatabaseStats struct {
	Project      string
	Database     string
	TenantCount  int
	BranchCount  int
	HotTenants   int
	TotalSize    int64
	LastUpdated  time.Time
}

// NewHierarchicalMetrics creates a new hierarchical metrics instance
func NewHierarchicalMetrics() *HierarchicalMetrics {
	return &HierarchicalMetrics{
		// System-wide metrics
		totalHotDBs: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "litestream_hot_databases_total",
			Help: "Total number of hot databases across all projects",
		}),
		totalColdDBs: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "litestream_cold_databases_total",
			Help: "Total number of cold databases across all projects",
		}),
		totalDBSize: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "litestream_db_size_bytes_total",
			Help: "Total size of all databases in bytes",
		}),
		totalWALSize: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "litestream_wal_size_bytes_total",
			Help: "Total size of all WAL files in bytes",
		}),
		totalWALBytes: promauto.NewCounter(prometheus.CounterOpts{
			Name: "litestream_wal_bytes_written_total",
			Help: "Total number of bytes written to shadow WAL",
		}),

		// Project-level metrics
		projectDBCount: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "litestream_project_databases",
			Help: "Number of databases per project",
		}, []string{"project"}),
		projectDBSize: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "litestream_project_size_bytes",
			Help: "Total size of databases in project",
		}, []string{"project"}),
		projectActiveDBs: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "litestream_project_active_databases",
			Help: "Number of active databases per project",
		}, []string{"project"}),
		projectSyncOps: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "litestream_project_sync_operations_total",
			Help: "Total sync operations per project",
		}, []string{"project"}),
		projectSyncDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "litestream_project_sync_duration_seconds",
			Help:    "Sync operation duration per project",
			Buckets: prometheus.DefBuckets,
		}, []string{"project"}),

		// Database-level metrics
		databaseTenantCount: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "litestream_database_tenants",
			Help: "Number of tenants per database",
		}, []string{"project", "database"}),
		databaseBranchCount: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "litestream_database_branches",
			Help: "Number of branches per database",
		}, []string{"project", "database"}),
		databaseHotTenants: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "litestream_database_hot_tenants",
			Help: "Number of hot tenants per database",
		}, []string{"project", "database"}),
		databaseSize: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Name: "litestream_database_size_bytes",
			Help: "Total size per database",
		}, []string{"project", "database"}),

		// Tier-based metrics
		tierSyncOps: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "litestream_tier_sync_operations_total",
			Help: "Total sync operations by tier",
		}, []string{"tier"}),
		tierSyncDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "litestream_tier_sync_duration_seconds",
			Help:    "Sync operation duration by tier",
			Buckets: prometheus.DefBuckets,
		}, []string{"tier"}),
		tierSyncErrors: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "litestream_tier_sync_errors_total",
			Help: "Total sync errors by tier",
		}, []string{"tier"}),
		tierWALBytes: promauto.NewCounterVec(prometheus.CounterOpts{
			Name: "litestream_tier_wal_bytes_total",
			Help: "Total WAL bytes by tier",
		}, []string{"tier"}),

		projectStats:  make(map[string]*ProjectStats),
		databaseStats: make(map[string]*DatabaseStats),
	}
}

// ParseDBPath extracts project, database, branch, and tenant from a database path
// Expected format: /path/to/project/databases/database/branches/branch/tenants/tenant.db
func ParseDBPath(path string) (project, database, branch, tenant string) {
	// Clean the path
	path = filepath.Clean(path)
	parts := strings.Split(path, string(filepath.Separator))

	// Find the indices of key directories
	for i := 0; i < len(parts); i++ {
		switch parts[i] {
		case "databases":
			if i > 0 {
				project = parts[i-1]
			}
			if i+1 < len(parts) {
				database = parts[i+1]
			}
		case "branches":
			if i+1 < len(parts) {
				branch = parts[i+1]
			}
		case "tenants":
			if i+1 < len(parts) {
				tenant = strings.TrimSuffix(parts[i+1], ".db")
			}
		}
	}

	// If pattern doesn't match, use simple extraction
	if project == "" {
		dir := filepath.Dir(path)
		project = filepath.Base(dir)
		database = "default"
		branch = "main"
		tenant = strings.TrimSuffix(filepath.Base(path), ".db")
	}

	return
}

// RecordDBMetrics records metrics for a database
func (m *HierarchicalMetrics) RecordDBMetrics(path string, size, walSize int64, isHot bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	project, database, _, _ := ParseDBPath(path)

	// Update project stats
	if _, ok := m.projectStats[project]; !ok {
		m.projectStats[project] = &ProjectStats{}
	}
	ps := m.projectStats[project]
	ps.TotalSize += size
	ps.TotalWALSize += walSize
	ps.LastUpdated = time.Now()

	// Update database stats
	dbKey := project + "/" + database
	if _, ok := m.databaseStats[dbKey]; !ok {
		m.databaseStats[dbKey] = &DatabaseStats{
			Project:  project,
			Database: database,
		}
	}
	ds := m.databaseStats[dbKey]
	ds.TotalSize += size
	ds.LastUpdated = time.Now()

	// Update metrics
	m.totalDBSize.Add(float64(size))
	m.totalWALSize.Add(float64(walSize))
	m.projectDBSize.WithLabelValues(project).Add(float64(size))
	m.databaseSize.WithLabelValues(project, database).Add(float64(size))

	if isHot {
		ps.ActiveDBs++
		ds.HotTenants++
	}
}

// RecordSync records a sync operation
func (m *HierarchicalMetrics) RecordSync(path string, duration time.Duration, bytes int64, isHot bool, err error) {
	project, _, _, _ := ParseDBPath(path)

	tier := "cold"
	if isHot {
		tier = "hot"
	}

	// Record tier metrics
	m.tierSyncOps.WithLabelValues(tier).Inc()
	m.tierSyncDuration.WithLabelValues(tier).Observe(duration.Seconds())
	if bytes > 0 {
		m.tierWALBytes.WithLabelValues(tier).Add(float64(bytes))
		m.totalWALBytes.Add(float64(bytes))
	}
	if err != nil {
		m.tierSyncErrors.WithLabelValues(tier).Inc()
	}

	// Record project metrics
	if project != "" {
		m.projectSyncOps.WithLabelValues(project).Inc()
		m.projectSyncDuration.WithLabelValues(project).Observe(duration.Seconds())
	}
}

// UpdateTierCounts updates the hot/cold database counts
func (m *HierarchicalMetrics) UpdateTierCounts(hotCount, coldCount int) {
	m.totalHotDBs.Set(float64(hotCount))
	m.totalColdDBs.Set(float64(coldCount))
}

// UpdateProjectStats updates aggregated project statistics
func (m *HierarchicalMetrics) UpdateProjectStats(project string, dbCount, activeCount int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.projectStats[project]; !ok {
		m.projectStats[project] = &ProjectStats{}
	}
	ps := m.projectStats[project]
	ps.TotalDBs = dbCount
	ps.ActiveDBs = activeCount
	ps.LastUpdated = time.Now()

	m.projectDBCount.WithLabelValues(project).Set(float64(dbCount))
	m.projectActiveDBs.WithLabelValues(project).Set(float64(activeCount))
}

// UpdateDatabaseStats updates aggregated database statistics
func (m *HierarchicalMetrics) UpdateDatabaseStats(project, database string, tenantCount, branchCount, hotTenants int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	dbKey := project + "/" + database
	if _, ok := m.databaseStats[dbKey]; !ok {
		m.databaseStats[dbKey] = &DatabaseStats{
			Project:  project,
			Database: database,
		}
	}
	ds := m.databaseStats[dbKey]
	ds.TenantCount = tenantCount
	ds.BranchCount = branchCount
	ds.HotTenants = hotTenants
	ds.LastUpdated = time.Now()

	m.databaseTenantCount.WithLabelValues(project, database).Set(float64(tenantCount))
	m.databaseBranchCount.WithLabelValues(project, database).Set(float64(branchCount))
	m.databaseHotTenants.WithLabelValues(project, database).Set(float64(hotTenants))
}