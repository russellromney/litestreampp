package litestreampp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WriteDetector handles write detection and hot/cold tier management
type WriteDetector struct {
	mu sync.RWMutex

	// Configuration
	scanInterval   time.Duration // How often to scan (15s)
	hotDuration    time.Duration // How long to keep hot after write (15s)
	maxHotDBs      int          // Maximum hot databases

	// State tracking
	databases      map[string]*WriteState
	hotList        []string // Ordered list of hot DBs for LRU

	// Callbacks
	onPromoteToHot func(path string) error
	onDemoteToCold func(path string) error

	// Shared resources
	sharedResources *SharedResourceManager
	connectionPool  *ConnectionPool

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// WriteState tracks write detection state for a database
type WriteState struct {
	Path        string
	LastModTime time.Time
	LastSize    int64
	IsHot       bool
	HotUntil    time.Time
	LastChecked time.Time
}

// NewWriteDetector creates a new write detector
func NewWriteDetector(scanInterval, hotDuration time.Duration, maxHotDBs int) *WriteDetector {
	return &WriteDetector{
		scanInterval: scanInterval,
		hotDuration:  hotDuration,
		maxHotDBs:    maxHotDBs,
		databases:    make(map[string]*WriteState),
		hotList:      make([]string, 0),
	}
}

// SetCallbacks sets the promotion/demotion callbacks
func (w *WriteDetector) SetCallbacks(onPromote, onDemote func(path string) error) {
	w.onPromoteToHot = onPromote
	w.onDemoteToCold = onDemote
}

// SetResources sets shared resources
func (w *WriteDetector) SetResources(shared *SharedResourceManager, connPool *ConnectionPool) {
	w.sharedResources = shared
	w.connectionPool = connPool
}

// Start begins the write detection loop
func (w *WriteDetector) Start(ctx context.Context) {
	w.ctx, w.cancel = context.WithCancel(ctx)

	w.wg.Add(1)
	go w.scanLoop()
}

// Stop stops the write detector
func (w *WriteDetector) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}

// scanLoop is the main detection loop
func (w *WriteDetector) scanLoop() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.scanInterval)
	defer ticker.Stop()

	// Initial scan
	w.performScan()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.performScan()
		}
	}
}

// performScan scans all databases for write activity
func (w *WriteDetector) performScan() {
	start := time.Now()
	now := time.Now()

	w.mu.Lock()
	defer w.mu.Unlock()

	var promoted, demoted int
	newHotList := make([]string, 0, len(w.hotList))

	// Check all tracked databases
	for path, state := range w.databases {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				// Database was deleted
				delete(w.databases, path)
				if state.IsHot {
					w.demoteToColLocked(path)
					demoted++
				}
			}
			continue
		}

		// Check for modifications
		modified := info.ModTime().After(state.LastModTime) || info.Size() != state.LastSize

		if modified {
			// Database was modified - promote to hot
			if !state.IsHot {
				if err := w.promoteToHotLocked(path); err != nil {
					slog.Error("failed to promote to hot", "path", path, "error", err)
				} else {
					promoted++
				}
			}
			state.IsHot = true
			state.HotUntil = now.Add(w.hotDuration)
			newHotList = append(newHotList, path)

			// Update tracking
			state.LastModTime = info.ModTime()
			state.LastSize = info.Size()
		} else if state.IsHot && now.After(state.HotUntil) {
			// No recent modifications and hot period expired - demote to cold
			if err := w.demoteToColLocked(path); err != nil {
				slog.Error("failed to demote to cold", "path", path, "error", err)
			} else {
				demoted++
			}
			state.IsHot = false
		} else if state.IsHot {
			// Still hot, keep in list
			newHotList = append(newHotList, path)
		}

		state.LastChecked = now
	}

	// Enforce max hot databases limit (LRU eviction)
	if len(newHotList) > w.maxHotDBs {
		// Sort by HotUntil time (oldest first)
		toEvict := len(newHotList) - w.maxHotDBs
		for i := 0; i < toEvict; i++ {
			path := newHotList[i]
			if state, ok := w.databases[path]; ok {
				if err := w.demoteToColLocked(path); err != nil {
					slog.Error("failed to evict hot database", "path", path, "error", err)
				} else {
					state.IsHot = false
					demoted++
				}
			}
		}
		newHotList = newHotList[toEvict:]
	}

	w.hotList = newHotList

	// Update metrics
	if GlobalMetrics != nil {
		GlobalMetrics.UpdateTierCounts(len(newHotList), len(w.databases)-len(newHotList))
	}

	slog.Debug("write detection scan complete",
		"duration", time.Since(start),
		"total", len(w.databases),
		"hot", len(newHotList),
		"promoted", promoted,
		"demoted", demoted)
}

// AddDatabase adds a database to track
func (w *WriteDetector) AddDatabase(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.databases[path]; exists {
		return nil // Already tracking
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat database: %w", err)
	}

	w.databases[path] = &WriteState{
		Path:        path,
		LastModTime: info.ModTime(),
		LastSize:    info.Size(),
		LastChecked: time.Now(),
	}

	return nil
}

// AddDatabases adds multiple databases from glob patterns
func (w *WriteDetector) AddDatabases(patterns []string) error {
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			slog.Error("glob pattern failed", "pattern", pattern, "error", err)
			continue
		}

		for _, path := range matches {
			if err := w.AddDatabase(path); err != nil {
				slog.Error("failed to add database", "path", path, "error", err)
			}
		}
	}

	return nil
}

// promoteToHotLocked promotes a database to hot tier (must hold lock)
func (w *WriteDetector) promoteToHotLocked(path string) error {
	if w.onPromoteToHot != nil {
		return w.onPromoteToHot(path)
	}
	return nil
}

// demoteToColLocked demotes a database to cold tier (must hold lock)
func (w *WriteDetector) demoteToColLocked(path string) error {
	if w.onDemoteToCold != nil {
		return w.onDemoteToCold(path)
	}
	return nil
}

// GetHotDatabases returns the current list of hot databases
func (w *WriteDetector) GetHotDatabases() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	result := make([]string, len(w.hotList))
	copy(result, w.hotList)
	return result
}

// GetStatistics returns current statistics
func (w *WriteDetector) GetStatistics() (total, hot, cold int) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	total = len(w.databases)
	hot = len(w.hotList)
	cold = total - hot
	return
}

// IsHot checks if a database is currently hot
func (w *WriteDetector) IsHot(path string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if state, ok := w.databases[path]; ok {
		return state.IsHot
	}
	return false
}