package litestreampp

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// ConnectionPool manages database connections with limits
type ConnectionPool struct {
	mu sync.RWMutex
	
	// Configuration
	maxConnections int
	idleTimeout    time.Duration
	
	// Active connections
	connections    map[string]*PooledConnection
	lru            *LRUCache
	
	// Metrics
	totalOpened    int64
	totalClosed    int64
	currentOpen    int
}

// PooledConnection wraps a database connection with metadata
type PooledConnection struct {
	db         *sql.DB
	path       string
	openedAt   time.Time
	lastUsed   time.Time
	useCount   int64
	
	// Cleanup function
	onClose    func() error
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool(maxConnections int, idleTimeout time.Duration) *ConnectionPool {
	return &ConnectionPool{
		maxConnections: maxConnections,
		idleTimeout:    idleTimeout,
		connections:    make(map[string]*PooledConnection),
		lru:           NewLRUCache(maxConnections),
	}
}

// Get returns a connection from the pool, opening if necessary
func (p *ConnectionPool) Get(path string) (*sql.DB, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	// Check if already open
	if conn, ok := p.connections[path]; ok {
		conn.lastUsed = time.Now()
		conn.useCount++
		p.lru.Touch(path)
		return conn.db, nil
	}
	
	// Check connection limit
	if p.currentOpen >= p.maxConnections {
		// Evict LRU connection
		if victim := p.lru.Evict(); victim != "" {
			p.closeConnectionLocked(victim)
		}
	}
	
	// Open new connection
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open connection: %w", err)
	}
	
	// Configure connection
	db.SetMaxOpenConns(1) // SQLite restriction
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // Managed by pool
	
	// Create pooled connection
	conn := &PooledConnection{
		db:       db,
		path:     path,
		openedAt: time.Now(),
		lastUsed: time.Now(),
		useCount: 1,
	}
	
	p.connections[path] = conn
	p.lru.Add(path)
	p.currentOpen++
	p.totalOpened++
	
	return db, nil
}

// Release marks a connection as no longer in use
func (p *ConnectionPool) Release(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if conn, ok := p.connections[path]; ok {
		conn.lastUsed = time.Now()
	}
}

// Close explicitly closes a connection
func (p *ConnectionPool) Close(path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	return p.closeConnectionLocked(path)
}

// closeConnectionLocked closes a connection (must hold lock)
func (p *ConnectionPool) closeConnectionLocked(path string) error {
	conn, ok := p.connections[path]
	if !ok {
		return nil
	}
	
	// Run cleanup if set
	if conn.onClose != nil {
		if err := conn.onClose(); err != nil {
			return fmt.Errorf("onClose callback: %w", err)
		}
	}
	
	// Close database
	if err := conn.db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	
	// Update tracking
	delete(p.connections, path)
	p.lru.Remove(path)
	p.currentOpen--
	p.totalClosed++
	
	return nil
}

// Cleanup closes idle connections
func (p *ConnectionPool) Cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	now := time.Now()
	toClose := []string{}
	
	for path, conn := range p.connections {
		if now.Sub(conn.lastUsed) > p.idleTimeout {
			toClose = append(toClose, path)
		}
	}
	
	for _, path := range toClose {
		p.closeConnectionLocked(path)
	}
}

// Start begins the cleanup goroutine
func (p *ConnectionPool) Start(ctx context.Context) {
	ticker := time.NewTicker(p.idleTimeout / 2)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.Cleanup()
		}
	}
}

// Stats returns pool statistics
func (p *ConnectionPool) Stats() ConnectionPoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	return ConnectionPoolStats{
		CurrentOpen:  p.currentOpen,
		TotalOpened:  p.totalOpened,
		TotalClosed:  p.totalClosed,
		MaxConnections: p.maxConnections,
	}
}

// ConnectionPoolStats contains pool statistics
type ConnectionPoolStats struct {
	CurrentOpen    int
	TotalOpened    int64
	TotalClosed    int64
	MaxConnections int
}

// Simple LRU cache implementation
type LRUCache struct {
	capacity int
	items    map[string]*lruItem
	head     *lruItem
	tail     *lruItem
}

type lruItem struct {
	key  string
	prev *lruItem
	next *lruItem
}

func NewLRUCache(capacity int) *LRUCache {
	return &LRUCache{
		capacity: capacity,
		items:    make(map[string]*lruItem),
	}
}

func (c *LRUCache) Add(key string) {
	if item, ok := c.items[key]; ok {
		c.moveToFront(item)
		return
	}
	
	item := &lruItem{key: key}
	c.items[key] = item
	
	if c.head == nil {
		c.head = item
		c.tail = item
	} else {
		item.next = c.head
		c.head.prev = item
		c.head = item
	}
}

func (c *LRUCache) Touch(key string) {
	if item, ok := c.items[key]; ok {
		c.moveToFront(item)
	}
}

func (c *LRUCache) Remove(key string) {
	if item, ok := c.items[key]; ok {
		c.removeItem(item)
		delete(c.items, key)
	}
}

func (c *LRUCache) Evict() string {
	if c.tail == nil {
		return ""
	}
	
	key := c.tail.key
	c.removeItem(c.tail)
	delete(c.items, key)
	
	return key
}

func (c *LRUCache) moveToFront(item *lruItem) {
	if item == c.head {
		return
	}
	
	c.removeItem(item)
	
	item.prev = nil
	item.next = c.head
	c.head.prev = item
	c.head = item
}

func (c *LRUCache) removeItem(item *lruItem) {
	if item.prev != nil {
		item.prev.next = item.next
	} else {
		c.head = item.next
	}
	
	if item.next != nil {
		item.next.prev = item.prev
	} else {
		c.tail = item.prev
	}
}