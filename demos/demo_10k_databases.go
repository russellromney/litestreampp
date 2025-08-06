// demo_10k_databases.go - Demo script for testing Litestream with 10,000 databases
// Run with: go run demo_10k_databases.go

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/benbjohnson/litestream"
	_ "github.com/mattn/go-sqlite3"
)

const (
	numProjects   = 10
	numDatabases  = 10
	numBranches   = 1
	numTenants    = 100 // 10 * 10 * 1 * 100 = 10,000 total databases
	
	// Simulation parameters
	writePercentage = 1   // 1% of databases are actively written to
	writeBurstSize  = 100 // Number of DBs to write in each burst
	writeInterval   = 5 * time.Second
)

// Stats tracks demo statistics
type Stats struct {
	totalDBs         int32
	activeWrites     int32
	totalWrites      int64
	promotions       int32
	demotions        int32
	memoryUsageMB    float64
	startTime        time.Time
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	fmt.Println("=== Litestream 10,000 Database Demo ===")
	fmt.Println()
	
	// Parse command line flags
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		printUsage()
		return
	}
	
	// Create demo directory - use disk-based storage, not /tmp which may be in memory
	homeDir, _ := os.UserHomeDir()
	demoDir := filepath.Join(homeDir, ".litestream-demos", "10k-demo")
	
	// Cleanup function
	cleanup := func() {
		fmt.Println("\nCleaning up demo files...")
		if err := os.RemoveAll(demoDir); err != nil {
			log.Printf("Warning: Failed to clean up %s: %v", demoDir, err)
		} else {
			fmt.Printf("✓ Cleanup complete - removed %s\n", demoDir)
		}
		// Also try to remove parent directory if empty
		parentDir := filepath.Dir(demoDir)
		os.Remove(parentDir) // Ignore error, will fail if not empty
	}
	
	// Ensure cleanup on exit
	defer cleanup()
	
	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	
	// Also cleanup any previous runs
	if err := os.RemoveAll(demoDir); err != nil {
		log.Printf("Warning: failed to clean previous demo directory: %v", err)
	}
	
	fmt.Printf("Setting up demo in: %s\n", demoDir)
	fmt.Println("Note: This will create ~1GB of actual database files on disk")
	fmt.Println()
	
	// Initialize stats
	stats := &Stats{
		startTime: time.Now(),
	}
	
	// Create databases
	fmt.Println("Creating 10,000 databases...")
	dbPaths := createDatabases(demoDir, stats)
	fmt.Printf("✓ Created %d databases\n", len(dbPaths))
	fmt.Println()
	
	// Setup Litestream
	fmt.Println("Initializing Litestream multi-database manager...")
	manager := setupLitestream(demoDir)
	
	// Start manager
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	if err := manager.Start(ctx); err != nil {
		log.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()
	
	fmt.Println("✓ Manager started")
	fmt.Println()
	
	// Start monitoring
	go monitorSystem(ctx, manager, stats)
	
	// Start write simulation
	fmt.Println("Starting write simulation...")
	fmt.Printf("- Writing to %d%% of databases (%d DBs)\n", writePercentage, writeBurstSize)
	fmt.Printf("- Write interval: %v\n", writeInterval)
	fmt.Println()
	
	go simulateWrites(ctx, dbPaths, stats)
	
	// Run demo
	fmt.Println("Demo running... Press Ctrl+C to stop")
	fmt.Println("=" + strings.Repeat("=", 50))
	fmt.Println()
	
	// Wait for interrupt (sigChan already created above)
	<-sigChan
	
	// Graceful shutdown
	fmt.Println("\n\nShutting down gracefully...")
	cancel()
	
	// Give time for goroutines to finish
	fmt.Println("Waiting for operations to complete...")
	time.Sleep(2 * time.Second)
	
	// Print final stats
	printFinalStats(stats, manager)
}

func createDatabases(baseDir string, stats *Stats) []string {
	// Use template + parallel copying for speed
	fmt.Println("Using optimized creation (template + parallel copy)...")
	
	// Create template database
	templatePath := filepath.Join(baseDir, "template.db")
	os.MkdirAll(filepath.Dir(templatePath), 0755)
	if err := createSQLiteDB(templatePath); err != nil {
		log.Fatalf("Failed to create template: %v", err)
	}
	
	// Read template into memory
	templateData, err := os.ReadFile(templatePath)
	if err != nil {
		log.Fatalf("Failed to read template: %v", err)
	}
	fmt.Printf("  Template size: %d bytes\n", len(templateData))
	
	// Create databases by copying template in parallel
	var paths []string
	var pathsMu sync.Mutex
	var wg sync.WaitGroup
	
	// Use worker pool for parallel copying
	workers := runtime.NumCPU() * 2
	taskChan := make(chan string, 1000)
	
	fmt.Printf("  Using %d workers for parallel creation\n", workers)
	
	// Start workers
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for dbPath := range taskChan {
				dir := filepath.Dir(dbPath)
				if err := os.MkdirAll(dir, 0755); err != nil {
					log.Printf("Failed to create directory: %v", err)
					continue
				}
				
				// Write template data to create database
				if err := os.WriteFile(dbPath, templateData, 0644); err != nil {
					log.Printf("Failed to create database: %v", err)
					continue
				}
				
				pathsMu.Lock()
				paths = append(paths, dbPath)
				pathsMu.Unlock()
				
				// Update progress
				if count := atomic.AddInt32(&stats.totalDBs, 1); count%1000 == 0 {
					fmt.Printf("  Created %d databases...\n", count)
				}
			}
		}()
	}
	
	// Generate all database paths
	for p := 0; p < numProjects; p++ {
		for d := 0; d < numDatabases; d++ {
			for b := 0; b < numBranches; b++ {
				for t := 0; t < numTenants; t++ {
					dbPath := filepath.Join(
						baseDir,
						fmt.Sprintf("project%d", p),
						"databases",
						fmt.Sprintf("db%d", d),
						"branches",
						fmt.Sprintf("branch%d", b),
						"tenants",
						fmt.Sprintf("tenant%d.db", t),
					)
					taskChan <- dbPath
				}
			}
		}
	}
	
	close(taskChan)
	wg.Wait()
	
	// Clean up template
	os.Remove(templatePath)
	
	return paths
}

func createSQLiteDB(path string) error {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer db.Close()
	
	// Create a simple table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS data (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			value TEXT,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}
	
	// Insert initial data
	_, err = db.Exec("INSERT INTO data (value) VALUES (?)", "initial")
	return err
}

func setupLitestream(baseDir string) *litestream.IntegratedMultiDBManager {
	// Create configuration
	config := &litestream.MultiDBConfig{
		Enabled:         true,
		Patterns: []string{
			filepath.Join(baseDir, "*", "databases", "*", "branches", "*", "tenants", "*.db"),
		},
		MaxHotDatabases: 1000, // Limit hot databases to 1000
		ScanInterval:    15 * time.Second,
		HotPromotion: litestream.HotPromotionConfig{
			RecentModifyThreshold: 15 * time.Second,
		},
	}
	
	// Create store
	store := litestream.NewStore(nil, litestream.CompactionLevels{})
	
	// Create manager
	manager, err := litestream.NewIntegratedMultiDBManager(store, config)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}
	
	return manager
}

func simulateWrites(ctx context.Context, dbPaths []string, stats *Stats) {
	ticker := time.NewTicker(writeInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Select random databases to write to
			selectedDBs := selectRandomDBs(dbPaths, writeBurstSize)
			
			var wg sync.WaitGroup
			for _, dbPath := range selectedDBs {
				wg.Add(1)
				go func(path string) {
					defer wg.Done()
					
					if err := writeToDatabase(path); err != nil {
						log.Printf("Write failed: %v", err)
						return
					}
					
					atomic.AddInt64(&stats.totalWrites, 1)
					atomic.AddInt32(&stats.activeWrites, 1)
					
					// Simulate write duration
					time.Sleep(100 * time.Millisecond)
					atomic.AddInt32(&stats.activeWrites, -1)
				}(dbPath)
			}
			
			wg.Wait()
		}
	}
}

func selectRandomDBs(dbPaths []string, count int) []string {
	if count > len(dbPaths) {
		count = len(dbPaths)
	}
	
	// Shuffle and select
	perm := rand.Perm(len(dbPaths))
	selected := make([]string, count)
	for i := 0; i < count; i++ {
		selected[i] = dbPaths[perm[i]]
	}
	
	return selected
}

func writeToDatabase(dbPath string) error {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	
	// Perform write operation
	_, err = db.Exec(
		"INSERT INTO data (value) VALUES (?)",
		fmt.Sprintf("write_%d", time.Now().Unix()),
	)
	
	return err
}

func monitorSystem(ctx context.Context, manager *litestream.IntegratedMultiDBManager, stats *Stats) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	lastPromotions := int32(0)
	lastDemotions := int32(0)
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Get current stats
			total, hot, cold, connStats := manager.GetStatistics()
			
			// Calculate memory usage
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			stats.memoryUsageMB = float64(m.Alloc) / 1024 / 1024
			
			// Calculate rates
			uptime := time.Since(stats.startTime).Seconds()
			writeRate := float64(atomic.LoadInt64(&stats.totalWrites)) / uptime
			
			// Estimate promotions/demotions (would need actual tracking)
			currentPromotions := int32(hot)
			currentDemotions := int32(cold)
			
			promotionDelta := currentPromotions - lastPromotions
			demotionDelta := currentDemotions - lastDemotions
			
			if promotionDelta > 0 {
				atomic.AddInt32(&stats.promotions, promotionDelta)
			}
			if demotionDelta > 0 {
				atomic.AddInt32(&stats.demotions, demotionDelta)
			}
			
			lastPromotions = currentPromotions
			lastDemotions = currentDemotions
			
			// Print status
			fmt.Printf("\r[%s] DBs: %d | Hot: %d | Cold: %d | Writes/s: %.1f | Mem: %.1f MB | Conns: %d",
				time.Now().Format("15:04:05"),
				total,
				hot,
				cold,
				writeRate,
				stats.memoryUsageMB,
				connStats.CurrentOpen,
			)
		}
	}
}

func printFinalStats(stats *Stats, manager *litestream.IntegratedMultiDBManager) {
	total, hot, cold, connStats := manager.GetStatistics()
	uptime := time.Since(stats.startTime)
	
	fmt.Println("\n=== Final Statistics ===")
	fmt.Printf("Runtime:              %v\n", uptime.Round(time.Second))
	fmt.Printf("Total Databases:      %d\n", total)
	fmt.Printf("Hot Databases:        %d\n", hot)
	fmt.Printf("Cold Databases:       %d\n", cold)
	fmt.Printf("Total Writes:         %d\n", atomic.LoadInt64(&stats.totalWrites))
	fmt.Printf("Write Rate:           %.2f/second\n", float64(atomic.LoadInt64(&stats.totalWrites))/uptime.Seconds())
	fmt.Printf("Memory Usage:         %.2f MB\n", stats.memoryUsageMB)
	fmt.Printf("Open Connections:     %d\n", connStats.CurrentOpen)
	fmt.Printf("Total Connections:    %d\n", connStats.TotalOpened)
	fmt.Println()
	
	// Memory efficiency calculation
	traditionalMemory := float64(total) * 0.5 // 500KB per DB traditional
	actualMemory := stats.memoryUsageMB
	savings := (1 - actualMemory/traditionalMemory) * 100
	
	fmt.Println("=== Memory Efficiency ===")
	fmt.Printf("Traditional Approach: %.2f MB\n", traditionalMemory)
	fmt.Printf("Optimized Approach:   %.2f MB\n", actualMemory)
	fmt.Printf("Memory Savings:       %.1f%%\n", savings)
}

func printUsage() {
	fmt.Println("Litestream 10,000 Database Demo")
	fmt.Println()
	fmt.Println("This demo creates 10,000 SQLite databases and demonstrates")
	fmt.Println("Litestream's ability to efficiently manage them with the new")
	fmt.Println("multi-database hot/cold tier system.")
	fmt.Println()
	fmt.Println("The demo will:")
	fmt.Println("  1. Create 10,000 databases in ~/.litestream-demos/10k-demo/")
	fmt.Println("  2. Use ~1GB of disk space for actual database files")
	fmt.Println("  3. Start the Litestream multi-database manager")
	fmt.Println("  4. Simulate writes to 1% of databases")
	fmt.Println("  5. Monitor and report performance metrics")
	fmt.Println()
	fmt.Println("Note: Databases are stored on disk, not in memory, for realistic testing")
	fmt.Println()
	fmt.Println("Usage: go run demo_10k_databases.go")
}