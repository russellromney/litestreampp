package litestreampp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/benbjohnson/litestream"
)

// DynamicDB wraps a regular DB with dynamic lifecycle management
type DynamicDB struct {
	*litestream.DB
	
	mu           sync.RWMutex
	state        DBLifecycleState
	lastAccess   time.Time
	accessCount  int64
	
	// Callbacks for state changes
	onOpen       func(*DynamicDB) error
	onClose      func(*DynamicDB) error
	
	// Parent manager (optional)
	manager      interface{}
}

// DBLifecycleState represents the current state of a database
type DBLifecycleState int

const (
	DBStateClosed DBLifecycleState = iota
	DBStateOpening
	DBStateOpen
	DBStateClosing
)

// NewDynamicDB creates a new dynamically managed database
func NewDynamicDB(path string, manager interface{}) *DynamicDB {
	db := litestream.NewDB(path)
	
	return &DynamicDB{
		DB:      db,
		state:   DBStateClosed,
		manager: manager,
	}
}

// Open initializes the database connection and starts replication
func (d *DynamicDB) Open(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	// Check current state
	switch d.state {
	case DBStateOpen:
		return nil // Already open
	case DBStateOpening:
		return fmt.Errorf("database is already opening")
	case DBStateClosing:
		return fmt.Errorf("database is closing")
	}
	
	d.state = DBStateOpening
	
	// Open the underlying database
	if err := d.DB.Open(); err != nil {
		d.state = DBStateClosed
		return fmt.Errorf("open database: %w", err)
	}
	
	d.state = DBStateOpen
	d.lastAccess = time.Now()
	
	// Call callback if set
	if d.onOpen != nil {
		if err := d.onOpen(d); err != nil {
			// Rollback on callback error
			d.DB.Close(ctx)
			d.state = DBStateClosed
			return fmt.Errorf("onOpen callback: %w", err)
		}
	}
	
	slog.Info("dynamically opened database", "path", d.Path())
	
	return nil
}

// Close shuts down the database and stops replication
func (d *DynamicDB) Close(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	// Check current state
	switch d.state {
	case DBStateClosed:
		return nil // Already closed
	case DBStateClosing:
		return fmt.Errorf("database is already closing")
	case DBStateOpening:
		return fmt.Errorf("database is opening")
	}
	
	d.state = DBStateClosing
	
	// Call callback if set
	if d.onClose != nil {
		if err := d.onClose(d); err != nil {
			slog.Error("onClose callback failed", "error", err)
		}
	}
	
	// Close the underlying database
	if err := d.DB.Close(ctx); err != nil {
		slog.Error("close database failed", "path", d.Path(), "error", err)
	}
	
	d.state = DBStateClosed
	
	slog.Info("dynamically closed database", "path", d.Path())
	
	return nil
}

// EnsureOpen opens the database if not already open
func (d *DynamicDB) EnsureOpen(ctx context.Context) error {
	d.mu.RLock()
	state := d.state
	d.mu.RUnlock()
	
	if state == DBStateOpen {
		d.updateAccess()
		return nil
	}
	
	return d.Open(ctx)
}

// updateAccess updates access tracking
func (d *DynamicDB) updateAccess() {
	d.mu.Lock()
	d.lastAccess = time.Now()
	d.accessCount++
	d.mu.Unlock()
}

// IsOpen returns true if the database is open
func (d *DynamicDB) IsOpen() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.state == DBStateOpen
}

// LastAccess returns the last access time
func (d *DynamicDB) LastAccess() time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.lastAccess
}

// AccessCount returns the total access count
func (d *DynamicDB) AccessCount() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.accessCount
}

// Override DB methods to ensure open state

// Sync ensures the database is open before syncing
func (d *DynamicDB) Sync(ctx context.Context) error {
	if err := d.EnsureOpen(ctx); err != nil {
		return err
	}
	return d.DB.Sync(ctx)
}

// Checkpoint ensures the database is open before checkpointing
func (d *DynamicDB) Checkpoint(ctx context.Context, mode string) error {
	if err := d.EnsureOpen(ctx); err != nil {
		return err
	}
	return d.DB.Checkpoint(ctx, mode)
}

// WriteSnapshot ensures the database is open before writing snapshot
// TODO: This method needs to be updated to use the correct DB API
// func (d *DynamicDB) WriteSnapshot(ctx context.Context, path string) error {
// 	if err := d.EnsureOpen(ctx); err != nil {
// 		return err
// 	}
// 	return d.DB.WriteSnapshot(ctx, path)
// }