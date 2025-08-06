package litestreampp_test

import (
	"testing"
	"time"

	"github.com/benbjohnson/litestream/litestreampp"
)

func TestParseDBPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantProj string
		wantDB   string
		wantBr   string
		wantTen  string
	}{
		{
			name:     "StandardPath",
			path:     "/data/myproject/databases/maindb/branches/develop/tenants/customer1.db",
			wantProj: "myproject",
			wantDB:   "maindb",
			wantBr:   "develop",
			wantTen:  "customer1",
		},
		{
			name:     "WithTrailingSlash",
			path:     "/data/proj2/databases/db2/branches/main/tenants/tenant2.db/",
			wantProj: "proj2",
			wantDB:   "db2",
			wantBr:   "main",
			wantTen:  "tenant2",
		},
		{
			name:     "SimplePath",
			path:     "/var/lib/simple.db",
			wantProj: "lib",
			wantDB:   "default",
			wantBr:   "main",
			wantTen:  "simple",
		},
		{
			name:     "DeepNesting",
			path:     "/home/user/projects/myapp/databases/primary/branches/feature-x/tenants/org123.db",
			wantProj: "myapp",
			wantDB:   "primary",
			wantBr:   "feature-x",
			wantTen:  "org123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, database, branch, tenant := litestreampp.ParseDBPath(tt.path)
			
			if project != tt.wantProj {
				t.Errorf("project = %q, want %q", project, tt.wantProj)
			}
			if database != tt.wantDB {
				t.Errorf("database = %q, want %q", database, tt.wantDB)
			}
			if branch != tt.wantBr {
				t.Errorf("branch = %q, want %q", branch, tt.wantBr)
			}
			if tenant != tt.wantTen {
				t.Errorf("tenant = %q, want %q", tenant, tt.wantTen)
			}
		})
	}
}

func TestHierarchicalMetrics(t *testing.T) {
	t.Run("RecordDBMetrics", func(t *testing.T) {
		// Use the global metrics instance to avoid duplicate registration
		metrics := litestreampp.GlobalMetrics
		
		// Record metrics for multiple databases
		paths := []struct {
			path    string
			size    int64
			walSize int64
			isHot   bool
		}{
			{"/data/proj1/databases/db1/branches/main/tenants/tenant1.db", 1024, 512, true},
			{"/data/proj1/databases/db1/branches/main/tenants/tenant2.db", 2048, 1024, true},
			{"/data/proj1/databases/db2/branches/main/tenants/tenant3.db", 4096, 2048, false},
			{"/data/proj2/databases/db1/branches/main/tenants/tenant1.db", 8192, 4096, false},
		}
		
		for _, p := range paths {
			metrics.RecordDBMetrics(p.path, p.size, p.walSize, p.isHot)
		}
		
		// Update tier counts
		metrics.UpdateTierCounts(2, 2)
		
		// Note: We can't easily verify Prometheus metrics without a full registry
		// but this tests that the methods don't panic
	})

	t.Run("RecordSync", func(t *testing.T) {
		metrics := litestreampp.GlobalMetrics
		
		// Record some sync operations
		path1 := "/data/proj1/databases/db1/branches/main/tenants/tenant1.db"
		path2 := "/data/proj2/databases/db1/branches/main/tenants/tenant2.db"
		
		metrics.RecordSync(path1, 100*time.Millisecond, 1024, true, nil)
		metrics.RecordSync(path2, 200*time.Millisecond, 2048, false, nil)
		metrics.RecordSync(path1, 50*time.Millisecond, 512, true, nil)
		
		// Record a failed sync
		metrics.RecordSync(path2, 500*time.Millisecond, 0, false, &testError{"sync failed"})
	})

	t.Run("UpdateStats", func(t *testing.T) {
		metrics := litestreampp.GlobalMetrics
		
		// Update project stats
		metrics.UpdateProjectStats("project1", 100, 10)
		metrics.UpdateProjectStats("project2", 200, 20)
		
		// Update database stats
		metrics.UpdateDatabaseStats("project1", "database1", 50, 3, 5)
		metrics.UpdateDatabaseStats("project1", "database2", 50, 2, 5)
		metrics.UpdateDatabaseStats("project2", "database1", 200, 5, 20)
	})
}

func TestHierarchicalMetricsIntegration(t *testing.T) {
	metrics := litestreampp.GlobalMetrics
	
	// Simulate a complete workflow
	basePath := "/data/myapp/databases/primary/branches"
	
	// Add multiple tenants across branches
	for branch := 1; branch <= 3; branch++ {
		branchName := "branch" + string(rune('0'+branch))
		for tenant := 1; tenant <= 10; tenant++ {
			path := basePath + "/" + branchName + "/tenants/tenant" + string(rune('0'+tenant)) + ".db"
			
			// Some are hot, some are cold
			isHot := (branch == 1 && tenant <= 5) || (branch == 2 && tenant <= 3)
			size := int64(1024 * tenant)
			walSize := int64(512 * tenant)
			
			metrics.RecordDBMetrics(path, size, walSize, isHot)
			
			// Simulate sync operations
			if isHot {
				metrics.RecordSync(path, time.Duration(10+tenant)*time.Millisecond, walSize, true, nil)
			}
		}
	}
	
	// Update aggregated stats
	metrics.UpdateTierCounts(8, 22) // 8 hot, 22 cold
	metrics.UpdateProjectStats("myapp", 30, 8)
	metrics.UpdateDatabaseStats("myapp", "primary", 30, 3, 8)
}