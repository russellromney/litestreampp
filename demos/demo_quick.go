// demo_quick.go - Quick demo script for testing Litestream with 100 databases
// Run with: go run demo_quick.go

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/benbjohnson/litestream"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	log.SetFlags(log.Ltime)
	fmt.Println("=== Litestream Quick Demo (100 databases) ===")
	fmt.Println()
	
	// Setup - use disk-based storage, not /tmp which may be in memory
	homeDir, _ := os.UserHomeDir()
	demoDir := filepath.Join(homeDir, ".litestream-demos", "quick-demo")
	
	// Cleanup function
	cleanup := func() {
		fmt.Println("\nCleaning up demo files...")
		if err := os.RemoveAll(demoDir); err != nil {
			log.Printf("Warning: Failed to clean up %s: %v", demoDir, err)
		} else {
			fmt.Println("✓ Cleanup complete - removed", demoDir)
		}
		// Also try to remove parent directory if empty
		parentDir := filepath.Dir(demoDir)
		os.Remove(parentDir) // Ignore error, will fail if not empty
	}
	
	// Ensure cleanup on exit
	defer cleanup()
	
	// Also cleanup any previous runs
	os.RemoveAll(demoDir)
	
	// Create databases
	fmt.Println("Creating 100 test databases...")
	dbPaths := createTestDatabases(demoDir, 100)
	fmt.Printf("✓ Created %d databases\n\n", len(dbPaths))
	
	// Setup Litestream
	fmt.Println("Starting Litestream manager...")
	manager := setupManager(demoDir)
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	if err := manager.Start(ctx); err != nil {
		log.Fatalf("Failed to start: %v", err)
	}
	defer manager.Stop()
	
	fmt.Println("✓ Manager started\n")
	
	// Initial stats
	printStats("Initial", manager)
	
	// Simulate activity
	fmt.Println("\nSimulating database activity...")
	
	for i := 0; i < 3; i++ {
		fmt.Printf("\nRound %d:\n", i+1)
		
		// Write to random databases
		selectedDBs := selectRandom(dbPaths, 10)
		fmt.Printf("  Writing to %d databases...\n", len(selectedDBs))
		
		for _, dbPath := range selectedDBs {
			writeData(dbPath, fmt.Sprintf("round_%d", i))
		}
		
		// Wait for detection
		time.Sleep(2 * time.Second)
		
		// Show stats
		printStats(fmt.Sprintf("  After round %d", i+1), manager)
		
		// Wait before next round
		time.Sleep(3 * time.Second)
	}
	
	// Final stats
	fmt.Println("\n=== Final Results ===")
	printDetailedStats(manager)
	
	fmt.Println("\n✓ Demo completed successfully!")
}

func createTestDatabases(baseDir string, count int) []string {
	var paths []string
	
	// Create directory structure
	for i := 0; i < count; i++ {
		project := i / 10
		database := i % 10
		
		dir := filepath.Join(
			baseDir,
			fmt.Sprintf("project%d", project),
			"databases",
			fmt.Sprintf("db%d", database),
			"branches",
			"main",
			"tenants",
		)
		
		os.MkdirAll(dir, 0755)
		
		dbPath := filepath.Join(dir, fmt.Sprintf("tenant%d.db", i))
		
		// Create SQLite database
		db, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			log.Printf("Failed to create %s: %v", dbPath, err)
			continue
		}
		
		_, err = db.Exec(`
			CREATE TABLE data (
				id INTEGER PRIMARY KEY,
				value TEXT,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			INSERT INTO data (value) VALUES ('initial');
		`)
		db.Close()
		
		if err != nil {
			log.Printf("Failed to initialize %s: %v", dbPath, err)
			continue
		}
		
		paths = append(paths, dbPath)
	}
	
	return paths
}

func setupManager(baseDir string) *litestream.IntegratedMultiDBManager {
	config := &litestream.MultiDBConfig{
		Enabled:         true,
		Patterns: []string{
			filepath.Join(baseDir, "*", "databases", "*", "branches", "*", "tenants", "*.db"),
		},
		MaxHotDatabases: 50,
		ScanInterval:    1 * time.Second, // Fast for demo
		HotPromotion: litestream.HotPromotionConfig{
			RecentModifyThreshold: 5 * time.Second, // Short for demo
		},
	}
	
	store := litestream.NewStore(nil, litestream.CompactionLevels{})
	manager, err := litestream.NewIntegratedMultiDBManager(store, config)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}
	
	return manager
}

func selectRandom(items []string, count int) []string {
	if count > len(items) {
		count = len(items)
	}
	
	selected := make([]string, count)
	perm := rand.Perm(len(items))
	for i := 0; i < count; i++ {
		selected[i] = items[perm[i]]
	}
	
	return selected
}

func writeData(dbPath string, value string) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Printf("Failed to open %s: %v", dbPath, err)
		return
	}
	defer db.Close()
	
	_, err = db.Exec("INSERT INTO data (value) VALUES (?)", value)
	if err != nil {
		log.Printf("Failed to write to %s: %v", dbPath, err)
	}
}

func printStats(label string, manager *litestream.IntegratedMultiDBManager) {
	total, hot, cold, connStats := manager.GetStatistics()
	
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memMB := float64(m.Alloc) / 1024 / 1024
	
	fmt.Printf("%s: Total=%d, Hot=%d, Cold=%d, Connections=%d, Memory=%.1fMB\n",
		label, total, hot, cold, connStats.CurrentOpen, memMB)
}

func printDetailedStats(manager *litestream.IntegratedMultiDBManager) {
	total, hot, cold, connStats := manager.GetStatistics()
	
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	fmt.Printf("Total Databases:      %d\n", total)
	fmt.Printf("Hot Databases:        %d\n", hot)
	fmt.Printf("Cold Databases:       %d\n", cold)
	fmt.Printf("Open Connections:     %d\n", connStats.CurrentOpen)
	fmt.Printf("Total Connections:    %d\n", connStats.TotalOpened)
	fmt.Printf("Memory Usage:         %.2f MB\n", float64(m.Alloc)/1024/1024)
	fmt.Printf("Goroutines:           %d\n", runtime.NumGoroutine())
	
	// Calculate efficiency
	traditionalMem := float64(total) * 0.5 // 500KB per DB
	actualMem := float64(m.Alloc) / 1024 / 1024
	savings := (1 - actualMem/traditionalMem) * 100
	
	fmt.Printf("\nMemory Efficiency:\n")
	fmt.Printf("  Traditional:        %.2f MB\n", traditionalMem)
	fmt.Printf("  Actual:             %.2f MB\n", actualMem)
	fmt.Printf("  Savings:            %.1f%%\n", savings)
}