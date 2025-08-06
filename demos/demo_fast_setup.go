// demo_fast_setup.go - Optimized demo with fast database creation
// Run with: go run demo_fast_setup.go

package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/benbjohnson/litestream"
	_ "github.com/mattn/go-sqlite3"
)

const (
	numProjects  = 10
	numDatabases = 10
	numBranches  = 1
	numTenants   = 100 // 10 * 10 * 1 * 100 = 10,000 total
)

func main() {
	log.SetFlags(log.Ltime)
	fmt.Println("=== Litestream Fast Setup Demo (10,000 databases) ===")
	fmt.Println()

	// Setup
	homeDir, _ := os.UserHomeDir()
	demoDir := filepath.Join(homeDir, ".litestream-demos", "fast-demo")

	// Cleanup function
	cleanup := func() {
		fmt.Println("\nCleaning up demo files...")
		if err := os.RemoveAll(demoDir); err != nil {
			log.Printf("Warning: Failed to clean up %s: %v", demoDir, err)
		} else {
			fmt.Printf("✓ Cleanup complete - removed %s\n", demoDir)
		}
		os.Remove(filepath.Dir(demoDir))
	}
	defer cleanup()

	// Clean previous runs
	os.RemoveAll(demoDir)

	fmt.Printf("Setting up demo in: %s\n", demoDir)
	fmt.Println("Using optimized creation methods for speed")
	fmt.Println()

	// Method selection
	method := "hybrid" // Options: "copy", "parallel", "hybrid"
	if len(os.Args) > 1 {
		method = os.Args[1]
	}

	start := time.Now()
	var dbPaths []string

	switch method {
	case "copy":
		fmt.Println("Method: Template copying (fastest)")
		dbPaths = createDatabasesByCopying(demoDir)
	case "parallel":
		fmt.Println("Method: Parallel creation")
		dbPaths = createDatabasesInParallel(demoDir)
	case "hybrid":
		fmt.Println("Method: Hybrid (copy + parallel)")
		dbPaths = createDatabasesHybrid(demoDir)
	default:
		fmt.Println("Method: Hybrid (default)")
		dbPaths = createDatabasesHybrid(demoDir)
	}

	elapsed := time.Since(start)
	fmt.Printf("✓ Created %d databases in %v\n", len(dbPaths), elapsed)
	fmt.Printf("  Rate: %.0f databases/second\n", float64(len(dbPaths))/elapsed.Seconds())
	fmt.Println()

	// Start Litestream manager
	fmt.Println("Starting Litestream manager...")
	manager := setupManager(demoDir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := manager.Start(ctx); err != nil {
		log.Fatalf("Failed to start: %v", err)
	}
	defer manager.Stop()

	fmt.Println("✓ Manager started\n")

	// Quick test
	fmt.Println("Testing hot/cold transitions...")
	testHotCold(dbPaths[:10])

	// Stats
	printStats(manager)

	fmt.Println("\n✓ Demo completed successfully!")
}

// Method 1: Create by copying a template (fastest)
func createDatabasesByCopying(baseDir string) []string {
	fmt.Println("Creating template database...")
	
	// Create template
	templatePath := filepath.Join(baseDir, "template.db")
	os.MkdirAll(filepath.Dir(templatePath), 0755)
	createTemplateDB(templatePath)
	
	// Read template into memory
	templateData, err := os.ReadFile(templatePath)
	if err != nil {
		log.Fatalf("Failed to read template: %v", err)
	}
	
	fmt.Printf("Template size: %d bytes\n", len(templateData))
	fmt.Println("Copying to create 10,000 databases...")
	
	var paths []string
	var created int32
	
	// Use goroutines to copy in parallel
	var wg sync.WaitGroup
	workers := runtime.NumCPU()
	taskChan := make(chan string, 100)
	
	// Start workers
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range taskChan {
				dir := filepath.Dir(path)
				if err := os.MkdirAll(dir, 0755); err != nil {
					continue
				}
				if err := os.WriteFile(path, templateData, 0644); err != nil {
					log.Printf("Failed to copy to %s: %v", path, err)
					continue
				}
				atomic.AddInt32(&created, 1)
				if c := atomic.LoadInt32(&created); c%1000 == 0 {
					fmt.Printf("  Created %d databases...\n", c)
				}
			}
		}()
	}
	
	// Generate all paths
	for p := 0; p < numProjects; p++ {
		for d := 0; d < numDatabases; d++ {
			for b := 0; b < numBranches; b++ {
				for t := 0; t < numTenants; t++ {
					path := filepath.Join(
						baseDir,
						fmt.Sprintf("project%d", p),
						"databases",
						fmt.Sprintf("db%d", d),
						"branches",
						fmt.Sprintf("branch%d", b),
						"tenants",
						fmt.Sprintf("tenant%d.db", t),
					)
					paths = append(paths, path)
					taskChan <- path
				}
			}
		}
	}
	
	close(taskChan)
	wg.Wait()
	
	os.Remove(templatePath) // Clean up template
	return paths
}

// Method 2: Create databases in parallel with worker pool
func createDatabasesInParallel(baseDir string) []string {
	fmt.Println("Creating databases with worker pool...")
	
	var paths []string
	var pathsMu sync.Mutex
	var created int32
	
	// Worker pool
	workers := runtime.NumCPU() * 2
	var wg sync.WaitGroup
	taskChan := make(chan string, 100)
	
	fmt.Printf("Using %d workers\n", workers)
	
	// Start workers
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range taskChan {
				if err := createSingleDB(path); err != nil {
					log.Printf("Failed to create %s: %v", path, err)
					continue
				}
				
				pathsMu.Lock()
				paths = append(paths, path)
				pathsMu.Unlock()
				
				if c := atomic.AddInt32(&created, 1); c%1000 == 0 {
					fmt.Printf("  Created %d databases...\n", c)
				}
			}
		}()
	}
	
	// Generate tasks
	for p := 0; p < numProjects; p++ {
		for d := 0; d < numDatabases; d++ {
			for b := 0; b < numBranches; b++ {
				for t := 0; t < numTenants; t++ {
					path := filepath.Join(
						baseDir,
						fmt.Sprintf("project%d", p),
						"databases",
						fmt.Sprintf("db%d", d),
						"branches",
						fmt.Sprintf("branch%d", b),
						"tenants",
						fmt.Sprintf("tenant%d.db", t),
					)
					taskChan <- path
				}
			}
		}
	}
	
	close(taskChan)
	wg.Wait()
	
	return paths
}

// Method 3: Hybrid - create one DB per project, then copy
func createDatabasesHybrid(baseDir string) []string {
	fmt.Println("Using hybrid approach: create + copy...")
	
	var allPaths []string
	var created int32
	
	// Create one template per project in parallel
	var wg sync.WaitGroup
	pathsChan := make(chan []string, numProjects)
	
	for p := 0; p < numProjects; p++ {
		wg.Add(1)
		go func(projectID int) {
			defer wg.Done()
			
			// Create template for this project
			templatePath := filepath.Join(baseDir, fmt.Sprintf("template_p%d.db", projectID))
			os.MkdirAll(filepath.Dir(templatePath), 0755)
			createTemplateDB(templatePath)
			
			// Read template
			templateData, err := os.ReadFile(templatePath)
			if err != nil {
				log.Printf("Failed to read template: %v", err)
				return
			}
			
			// Copy to all locations in this project
			var projectPaths []string
			for d := 0; d < numDatabases; d++ {
				for b := 0; b < numBranches; b++ {
					for t := 0; t < numTenants; t++ {
						path := filepath.Join(
							baseDir,
							fmt.Sprintf("project%d", projectID),
							"databases",
							fmt.Sprintf("db%d", d),
							"branches",
							fmt.Sprintf("branch%d", b),
							"tenants",
							fmt.Sprintf("tenant%d.db", t),
						)
						
						dir := filepath.Dir(path)
						os.MkdirAll(dir, 0755)
						
						if err := os.WriteFile(path, templateData, 0644); err != nil {
							continue
						}
						
						projectPaths = append(projectPaths, path)
						
						if c := atomic.AddInt32(&created, 1); c%1000 == 0 {
							fmt.Printf("  Created %d databases...\n", c)
						}
					}
				}
			}
			
			os.Remove(templatePath) // Clean up template
			pathsChan <- projectPaths
		}(p)
	}
	
	// Collect results
	go func() {
		wg.Wait()
		close(pathsChan)
	}()
	
	for paths := range pathsChan {
		allPaths = append(allPaths, paths...)
	}
	
	return allPaths
}

// Helper to create template database
func createTemplateDB(path string) error {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer db.Close()
	
	_, err = db.Exec(`
		CREATE TABLE data (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			value TEXT,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX idx_timestamp ON data(timestamp);
		INSERT INTO data (value) VALUES ('initial');
	`)
	
	return err
}

// Helper to create a single database
func createSingleDB(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer db.Close()
	
	_, err = db.Exec(`
		CREATE TABLE data (
			id INTEGER PRIMARY KEY,
			value TEXT,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO data (value) VALUES ('initial');
	`)
	
	return err
}

// Helper to copy file
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	
	os.MkdirAll(filepath.Dir(dst), 0755)
	
	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()
	
	_, err = io.Copy(destFile, sourceFile)
	return err
}

func setupManager(baseDir string) *litestream.IntegratedMultiDBManager {
	config := &litestream.MultiDBConfig{
		Enabled:         true,
		Patterns: []string{
			filepath.Join(baseDir, "*", "databases", "*", "branches", "*", "tenants", "*.db"),
		},
		MaxHotDatabases: 1000,
		ScanInterval:    15 * time.Second,
		HotPromotion: litestream.HotPromotionConfig{
			RecentModifyThreshold: 15 * time.Second,
		},
	}
	
	store := litestream.NewStore(nil, litestream.CompactionLevels{})
	manager, err := litestream.NewIntegratedMultiDBManager(store, config)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}
	
	return manager
}

func testHotCold(dbPaths []string) {
	for i, path := range dbPaths {
		db, err := sql.Open("sqlite3", path)
		if err != nil {
			continue
		}
		db.Exec("INSERT INTO data (value) VALUES (?)", fmt.Sprintf("test_%d", i))
		db.Close()
	}
	time.Sleep(2 * time.Second)
}

func printStats(manager *litestream.IntegratedMultiDBManager) {
	total, hot, cold, connStats := manager.GetStatistics()
	
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	fmt.Println("\n=== Statistics ===")
	fmt.Printf("Total Databases:  %d\n", total)
	fmt.Printf("Hot Databases:    %d\n", hot)
	fmt.Printf("Cold Databases:   %d\n", cold)
	fmt.Printf("Memory Usage:     %.2f MB\n", float64(m.Alloc)/1024/1024)
	fmt.Printf("Goroutines:       %d\n", runtime.NumGoroutine())
	fmt.Printf("Connections:      %d\n", connStats.CurrentOpen)
}

func printUsage() {
	fmt.Println("Usage: go run demo_fast_setup.go [method]")
	fmt.Println()
	fmt.Println("Methods:")
	fmt.Println("  copy     - Copy template database (fastest)")
	fmt.Println("  parallel - Create in parallel with workers")
	fmt.Println("  hybrid   - Hybrid approach (default)")
}