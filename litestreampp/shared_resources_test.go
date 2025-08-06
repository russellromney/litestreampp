package litestreampp_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/benbjohnson/litestream/litestreampp"
)

func TestWorkerPool(t *testing.T) {
	t.Run("ProcessesTasks", func(t *testing.T) {
		pool := litestreampp.NewWorkerPool("test", 5)
		defer pool.Stop()

		var counter int32
		var wg sync.WaitGroup
		
		// Submit 100 tasks
		for i := 0; i < 100; i++ {
			wg.Add(1)
			pool.Submit(&testTask{
				id: i,
				counter: &counter,
				wg: &wg,
			})
		}
		
		// Wait for all tasks to complete
		wg.Wait()
		
		// Verify all tasks were processed
		if got := atomic.LoadInt32(&counter); got != 100 {
			t.Errorf("expected 100 tasks processed, got %d", got)
		}
	})

	t.Run("HandlesErrors", func(t *testing.T) {
		pool := litestreampp.NewWorkerPool("test", 2)
		defer pool.Stop()

		var errorCount int32
		var wg sync.WaitGroup
		
		// Submit tasks that will error
		for i := 0; i < 10; i++ {
			wg.Add(1)
			pool.Submit(&errorTask{
				errorCount: &errorCount,
				wg: &wg,
			})
		}
		
		wg.Wait()
		
		// Verify errors were handled
		if got := atomic.LoadInt32(&errorCount); got != 10 {
			t.Errorf("expected 10 errors handled, got %d", got)
		}
	})

	t.Run("ConcurrentSubmission", func(t *testing.T) {
		pool := litestreampp.NewWorkerPool("test", 10)
		defer pool.Stop()

		var counter int32
		var wg sync.WaitGroup
		
		// 10 goroutines submitting 10 tasks each
		for g := 0; g < 10; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 10; i++ {
					pool.Submit(&testTask{
						id: i,
						counter: &counter,
						wg: nil,
						immediate: true,
					})
				}
			}()
		}
		
		wg.Wait()
		time.Sleep(100 * time.Millisecond) // Allow tasks to complete
		
		// Verify all tasks were processed
		if got := atomic.LoadInt32(&counter); got != 100 {
			t.Errorf("expected 100 tasks processed, got %d", got)
		}
	})
}

func TestTTLCache(t *testing.T) {
	t.Run("StoresAndRetrieves", func(t *testing.T) {
		cache := litestreampp.NewTTLCache(100 * time.Millisecond)
		
		cache.Set("key1", "value1", 1*time.Second)
		cache.Set("key2", "value2", 1*time.Second)
		
		// Retrieve immediately
		if val, ok := cache.Get("key1"); !ok || val != "value1" {
			t.Errorf("expected value1, got %v, ok=%v", val, ok)
		}
		
		if val, ok := cache.Get("key2"); !ok || val != "value2" {
			t.Errorf("expected value2, got %v, ok=%v", val, ok)
		}
	})

	t.Run("ExpiresItems", func(t *testing.T) {
		cache := litestreampp.NewTTLCache(50 * time.Millisecond)
		
		cache.Set("key1", "value1", 100*time.Millisecond)
		
		// Should exist immediately
		if _, ok := cache.Get("key1"); !ok {
			t.Error("expected key1 to exist")
		}
		
		// Wait for expiration
		time.Sleep(150 * time.Millisecond)
		
		// Should be expired
		if _, ok := cache.Get("key1"); ok {
			t.Error("expected key1 to be expired")
		}
	})

	t.Run("CleanupRemovesExpired", func(t *testing.T) {
		cache := litestreampp.NewTTLCache(50 * time.Millisecond)
		
		// Add items with short TTL
		for i := 0; i < 10; i++ {
			key := string(rune('a' + i))
			cache.Set(key, i, 75*time.Millisecond)
		}
		
		// Wait for cleanup cycle
		time.Sleep(150 * time.Millisecond)
		
		// All items should be gone
		for i := 0; i < 10; i++ {
			key := string(rune('a' + i))
			if _, ok := cache.Get(key); ok {
				t.Errorf("expected %s to be cleaned up", key)
			}
		}
	})
}

func TestAggregatedMetrics(t *testing.T) {
	t.Run("RecordsMetrics", func(t *testing.T) {
		metrics := litestreampp.NewAggregatedMetrics()
		
		// Record some operations
		metrics.RecordSync("hot", 100*time.Millisecond, 1024)
		metrics.RecordSync("cold", 200*time.Millisecond, 2048)
		metrics.RecordSync("hot", 150*time.Millisecond, 512)
		
		// Update tier counts
		metrics.UpdateTierCounts(100, 900)
		
		// Note: Can't easily verify Prometheus metrics without setting up
		// a full metrics registry, but this tests that the methods don't panic
	})
}

func TestSharedResourceManager(t *testing.T) {
	t.Run("BufferPool", func(t *testing.T) {
		mgr := litestreampp.NewSharedResourceManager()
		
		// Get buffer
		buf1 := mgr.GetBuffer()
		if len(buf1) != 8192 {
			t.Errorf("expected 8192 byte buffer, got %d", len(buf1))
		}
		
		// Return buffer
		mgr.PutBuffer(buf1)
		
		// Get again - should reuse
		buf2 := mgr.GetBuffer()
		if len(buf2) != 8192 {
			t.Errorf("expected 8192 byte buffer, got %d", len(buf2))
		}
		
		// Buffers might be the same (reused)
		if &buf1[0] != &buf2[0] {
			t.Log("Buffer was not reused (this is OK)")
		}
	})
}

// Test task implementation
type testTask struct {
	id        int
	counter   *int32
	wg        *sync.WaitGroup
	immediate bool
}

func (t *testTask) Execute() error {
	atomic.AddInt32(t.counter, 1)
	if !t.immediate && t.wg != nil {
		t.wg.Done()
	}
	return nil
}

func (t *testTask) OnError(err error) {
	// Not used in success case
}

// Error task implementation
type errorTask struct {
	errorCount *int32
	wg         *sync.WaitGroup
}

func (t *errorTask) Execute() error {
	return &testError{"intentional error"}
}

func (t *errorTask) OnError(err error) {
	atomic.AddInt32(t.errorCount, 1)
	t.wg.Done()
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}