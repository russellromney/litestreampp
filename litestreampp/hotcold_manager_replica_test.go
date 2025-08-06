package litestreampp

import (
	"context"
	"database/sql"
	"io"
	"testing"
	"time"

	"github.com/benbjohnson/litestream"
	_ "github.com/mattn/go-sqlite3"
	"github.com/superfly/ltx"
)

// MockReplicaClient is a mock implementation of ReplicaClient for testing
type MockReplicaClient struct {
	Type_         string
	InitCalled    int
	SyncCalled    int
	WriteCalled   int
	DeleteCalled  int
	LTXFilesCalls []ltx.TXID
	WrittenFiles  []struct {
		Level   int
		MinTXID ltx.TXID
		MaxTXID ltx.TXID
	}
}

func (c *MockReplicaClient) Type() string {
	return c.Type_
}

func (c *MockReplicaClient) Init(ctx context.Context) error {
	c.InitCalled++
	return nil
}

func (c *MockReplicaClient) LTXFiles(ctx context.Context, level int, seek ltx.TXID) (ltx.FileIterator, error) {
	c.LTXFilesCalls = append(c.LTXFilesCalls, seek)
	// Return a mock iterator that immediately returns no more files
	return &mockFileIterator{}, nil
}

// mockFileIterator is a mock implementation of ltx.FileIterator
type mockFileIterator struct{}

func (i *mockFileIterator) Next() bool { return false }
func (i *mockFileIterator) Err() error { return nil }
func (i *mockFileIterator) Item() *ltx.FileInfo { return nil }
func (i *mockFileIterator) Close() error { return nil }

func (c *MockReplicaClient) OpenLTXFile(ctx context.Context, level int, minTXID, maxTXID ltx.TXID) (io.ReadCloser, error) {
	return nil, io.EOF
}

func (c *MockReplicaClient) WriteLTXFile(ctx context.Context, level int, minTXID, maxTXID ltx.TXID, r io.Reader) (*ltx.FileInfo, error) {
	c.WriteCalled++
	c.WrittenFiles = append(c.WrittenFiles, struct {
		Level   int
		MinTXID ltx.TXID
		MaxTXID ltx.TXID
	}{level, minTXID, maxTXID})
	
	return &ltx.FileInfo{
		Level:   level,
		MinTXID: minTXID,
		MaxTXID: maxTXID,
	}, nil
}

func (c *MockReplicaClient) DeleteLTXFiles(ctx context.Context, a []*ltx.FileInfo) error {
	c.DeleteCalled++
	return nil
}

func (c *MockReplicaClient) DeleteAll(ctx context.Context) error {
	return nil
}

// MockReplicaClientFactory creates mock replica clients for testing
type MockReplicaClientFactory struct {
	CreateClientCalled int
	LastPath          string
	MockClient        *MockReplicaClient
}

func (f *MockReplicaClientFactory) CreateClient(config *ReplicaConfig, path string) (litestream.ReplicaClient, error) {
	f.CreateClientCalled++
	f.LastPath = path
	
	if f.MockClient == nil {
		f.MockClient = &MockReplicaClient{Type_: "mock"}
	}
	
	return f.MockClient, nil
}

func TestHotColdManagerWithReplica(t *testing.T) {
	// Create a temporary directory for testing
	dir := t.TempDir()
	
	// Create mock factory
	mockFactory := &MockReplicaClientFactory{
		MockClient: &MockReplicaClient{Type_: "mock"},
	}
	
	// Create replica template
	replicaTemplate := &ReplicaConfig{
		Type:         "mock",
		Path:         "test/{{project}}/{{database}}",
		SyncInterval: 1 * time.Second,
	}
	
	// Create store
	store := litestream.NewStore(nil, litestream.CompactionLevels{})
	
	// Create shared resources
	sharedResources := NewSharedResourceManager()
	
	// Create connection pool
	connectionPool := NewConnectionPool(10, 5*time.Second)
	
	// Create hot/cold manager with replica support
	config := &HotColdConfig{
		MaxHotDatabases: 10,
		ScanInterval:    1 * time.Second,
		HotDuration:     5 * time.Second,
		Store:           store,
		SharedResources: sharedResources,
		ConnectionPool:  connectionPool,
		ReplicaTemplate: replicaTemplate,
		ReplicaFactory:  mockFactory,
	}
	
	manager := NewHotColdManager(config)
	
	// Start manager
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("failed to start manager: %v", err)
	}
	defer manager.Stop()
	
	// Test promotion with replica creation
	testDBPath := dir + "/test.db"
	
	// Create a test database file
	if err := createTestDB(testDBPath); err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	
	// Promote to hot (should create replica)
	if err := manager.promoteToHot(testDBPath); err != nil {
		t.Fatalf("failed to promote to hot: %v", err)
	}
	
	// Verify replica was created
	if mockFactory.CreateClientCalled != 1 {
		t.Errorf("expected CreateClient to be called once, got %d", mockFactory.CreateClientCalled)
	}
	
	// Verify database is hot
	if !manager.IsHot(testDBPath) {
		t.Error("expected database to be hot")
	}
	
	// Verify replica exists in map
	manager.mu.RLock()
	replica, exists := manager.hotReplicas[testDBPath]
	manager.mu.RUnlock()
	
	if !exists {
		t.Error("expected replica to exist in hotReplicas map")
	}
	
	if replica == nil {
		t.Error("expected non-nil replica")
	}
	
	// Test demotion (should stop replica)
	if err := manager.demoteToCold(testDBPath); err != nil {
		t.Fatalf("failed to demote to cold: %v", err)
	}
	
	// Verify database is no longer hot
	if manager.IsHot(testDBPath) {
		t.Error("expected database to be cold after demotion")
	}
	
	// Verify replica was removed
	manager.mu.RLock()
	_, exists = manager.hotReplicas[testDBPath]
	manager.mu.RUnlock()
	
	if exists {
		t.Error("expected replica to be removed from hotReplicas map")
	}
}

// createTestDB creates a simple SQLite database for testing
func createTestDB(path string) error {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer db.Close()
	
	_, err = db.Exec(`CREATE TABLE test (id INTEGER PRIMARY KEY, value TEXT)`)
	return err
}

func TestHotColdManagerPathTemplateExpansion(t *testing.T) {
	manager := &HotColdManager{}
	
	tests := []struct {
		name     string
		template string
		dbPath   string
		expected string
	}{
		{
			name:     "simple project/database",
			template: "backup/{{project}}/{{database}}",
			dbPath:   "/data/project1/databases/db1/database.db",
			expected: "backup/project1/db1",
		},
		{
			name:     "with branch and tenant",
			template: "{{project}}/{{database}}/{{branch}}/{{tenant}}",
			dbPath:   "/data/project1/databases/db1/branches/main/tenants/tenant1.db",
			expected: "project1/db1/main/tenant1",
		},
		{
			name:     "with filename",
			template: "backups/{{filename}}/data",
			dbPath:   "/path/to/mydb.sqlite",
			expected: "backups/mydb/data",
		},
		{
			name:     "empty template",
			template: "",
			dbPath:   "/any/path.db",
			expected: "",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.expandPathTemplate(tt.template, tt.dbPath)
			if result != tt.expected {
				t.Errorf("expandPathTemplate(%q, %q) = %q, want %q", 
					tt.template, tt.dbPath, result, tt.expected)
			}
		})
	}
}