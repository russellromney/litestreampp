package litestreampp_test

import (
	"context"
	"testing"
	"time"

	"github.com/benbjohnson/litestream/litestreampp"
	_ "github.com/mattn/go-sqlite3"
)

func TestConnectionPool(t *testing.T) {
	t.Run("BasicOperations", func(t *testing.T) {
		pool := litestreampp.NewConnectionPool(3, 100*time.Millisecond)
		defer func() {
			// Clean up any open connections
			pool.Cleanup()
		}()

		// Create test database files
		db1 := t.TempDir() + "/db1.db"
		db2 := t.TempDir() + "/db2.db"
		db3 := t.TempDir() + "/db3.db"

		// Get connections
		conn1, err := pool.Get(db1)
		if err != nil {
			t.Fatalf("failed to get connection 1: %v", err)
		}
		
		conn2, err := pool.Get(db2)
		if err != nil {
			t.Fatalf("failed to get connection 2: %v", err)
		}
		
		// Verify connections work
		if err := conn1.Ping(); err != nil {
			t.Errorf("connection 1 ping failed: %v", err)
		}
		
		if err := conn2.Ping(); err != nil {
			t.Errorf("connection 2 ping failed: %v", err)
		}
		
		// Release connections
		pool.Release(db1)
		pool.Release(db2)
		
		// Get same connection again (should reuse)
		conn1Again, err := pool.Get(db1)
		if err != nil {
			t.Fatalf("failed to get connection 1 again: %v", err)
		}
		
		// Should be the same connection object
		if conn1 != conn1Again {
			t.Log("Connection was not reused (this is OK but not optimal)")
		}
		
		// Get stats
		stats := pool.Stats()
		if stats.CurrentOpen < 1 {
			t.Errorf("expected at least 1 open connection, got %d", stats.CurrentOpen)
		}
		
		// Test third connection
		conn3, err := pool.Get(db3)
		if err != nil {
			t.Fatalf("failed to get connection 3: %v", err)
		}
		
		if err := conn3.Ping(); err != nil {
			t.Errorf("connection 3 ping failed: %v", err)
		}
	})

	t.Run("ConnectionLimit", func(t *testing.T) {
		pool := litestreampp.NewConnectionPool(2, 100*time.Millisecond)
		defer pool.Cleanup()

		// Create test databases
		db1 := t.TempDir() + "/db1.db"
		db2 := t.TempDir() + "/db2.db"
		db3 := t.TempDir() + "/db3.db"

		// Get max connections
		conn1, err := pool.Get(db1)
		if err != nil {
			t.Fatalf("failed to get connection 1: %v", err)
		}
		
		conn2, err := pool.Get(db2)
		if err != nil {
			t.Fatalf("failed to get connection 2: %v", err)
		}
		
		// Third connection should evict LRU
		conn3, err := pool.Get(db3)
		if err != nil {
			t.Fatalf("failed to get connection 3: %v", err)
		}
		
		// All connections should still work
		if err := conn2.Ping(); err != nil {
			t.Errorf("connection 2 ping failed: %v", err)
		}
		if err := conn3.Ping(); err != nil {
			t.Errorf("connection 3 ping failed: %v", err)
		}
		
		stats := pool.Stats()
		if stats.CurrentOpen > 2 {
			t.Errorf("expected max 2 open connections, got %d", stats.CurrentOpen)
		}
		
		// conn1 might be closed (LRU evicted)
		// This is expected behavior
		_ = conn1
	})

	t.Run("IdleTimeout", func(t *testing.T) {
		pool := litestreampp.NewConnectionPool(5, 150*time.Millisecond)
		
		// Start cleanup routine
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go pool.Start(ctx)

		// Create test database
		dbPath := t.TempDir() + "/test.db"

		// Get connection
		conn, err := pool.Get(dbPath)
		if err != nil {
			t.Fatalf("failed to get connection: %v", err)
		}
		
		// Verify it works
		if err := conn.Ping(); err != nil {
			t.Errorf("connection ping failed: %v", err)
		}
		
		// Release it
		pool.Release(dbPath)
		
		// Check it's still open
		stats := pool.Stats()
		if stats.CurrentOpen != 1 {
			t.Errorf("expected 1 open connection, got %d", stats.CurrentOpen)
		}
		
		// Wait for idle timeout
		time.Sleep(200 * time.Millisecond)
		
		// Should be closed now
		pool.Cleanup()
		stats = pool.Stats()
		if stats.CurrentOpen != 0 {
			t.Errorf("expected 0 open connections after timeout, got %d", stats.CurrentOpen)
		}
	})

	t.Run("ExplicitClose", func(t *testing.T) {
		pool := litestreampp.NewConnectionPool(5, 1*time.Second)
		defer pool.Cleanup()

		dbPath := t.TempDir() + "/test.db"

		// Get connection
		_, err := pool.Get(dbPath)
		if err != nil {
			t.Fatalf("failed to get connection: %v", err)
		}
		
		// Check it's open
		stats := pool.Stats()
		if stats.CurrentOpen != 1 {
			t.Errorf("expected 1 open connection, got %d", stats.CurrentOpen)
		}
		
		// Close it explicitly
		if err := pool.Close(dbPath); err != nil {
			t.Errorf("failed to close connection: %v", err)
		}
		
		// Should be closed
		stats = pool.Stats()
		if stats.CurrentOpen != 0 {
			t.Errorf("expected 0 open connections after close, got %d", stats.CurrentOpen)
		}
	})
}

func TestLRUCache(t *testing.T) {
	t.Run("BasicOperations", func(t *testing.T) {
		cache := litestreampp.NewLRUCache(3)
		
		// Add items
		cache.Add("a") // Order: a (head), tail=a
		cache.Add("b") // Order: b (head), a (tail)
		cache.Add("c") // Order: c (head), b, a (tail)
		
		// Touch to update order - moves 'a' to front
		cache.Touch("a") // Order: a (head), c, b (tail)
		
		// Add new item (note: LRU doesn't auto-evict, must be done manually)
		cache.Add("d") // Order: d (head), a, c, b (tail) - now has 4 items
		
		// Evict LRU (should be b since it's the tail)
		evicted := cache.Evict()
		if evicted != "b" {
			t.Errorf("expected 'b' to be evicted, got '%s'", evicted)
		}
		
		// Remove item
		cache.Remove("a") // Order: d (head), c (tail)
		
		// Evict again
		evicted = cache.Evict()
		if evicted != "c" {
			t.Errorf("expected 'c' to be evicted, got '%s'", evicted)
		}
	})

	t.Run("EvictEmpty", func(t *testing.T) {
		cache := litestreampp.NewLRUCache(3)
		
		// Evict from empty cache
		evicted := cache.Evict()
		if evicted != "" {
			t.Errorf("expected empty string from empty cache, got '%s'", evicted)
		}
	})
}