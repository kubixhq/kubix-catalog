//go:build integration

package catalog_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/kubixhq/kubix-catalog/internal/catalog"
	"github.com/kubixhq/kubix-catalog/internal/db"
)

// testDB returns a connected *sql.DB using TEST_DB_DSN, or skips if not set.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set — skipping integration test")
	}
	d, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.PingContext(ctx); err != nil {
		d.Close()
		t.Fatalf("ping: %v", err)
	}
	if err := db.Migrate(d); err != nil {
		d.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// newStore creates a Store backed by a real DB and registers cleanup.
func newStore(t *testing.T) (*catalog.Store, *sql.DB) {
	t.Helper()
	d := testDB(t)
	s := catalog.NewStore(d)
	t.Cleanup(func() { truncateTables(d) })
	return s, d
}

func truncateTables(d *sql.DB) {
	// reverse dependency order
	_, _ = d.Exec("DELETE FROM catalog_endpoints")
	_, _ = d.Exec("DELETE FROM catalog_dependencies")
	_, _ = d.Exec("DELETE FROM catalog_specs")
	_, _ = d.Exec("DELETE FROM catalog_services")
}

// mustIngest is a helper that ingests a spec and fails the test on error.
func mustIngest(t *testing.T, s *catalog.Store, serviceName, version string, endpoints []catalog.Endpoint) int {
	t.Helper()
	id, err := s.IngestSpec(serviceName, "", version, endpoints, []byte("{}"))
	if err != nil {
		t.Fatalf("IngestSpec(%q, %q): %v", serviceName, version, err)
	}
	return id
}

// ep builds a minimal endpoint for test use.
func ep(method, path string) catalog.Endpoint {
	return catalog.Endpoint{Method: method, Path: path}
}

// epDesc builds an endpoint with description.
func epDesc(method, path, desc string) catalog.Endpoint {
	return catalog.Endpoint{Method: method, Path: path, Description: desc}
}

// epTagged builds an endpoint with tags.
func epTagged(method, path string, tags ...string) catalog.Endpoint {
	return catalog.Endpoint{Method: method, Path: path, Tags: tags}
}

// minSpec builds a minimal valid OpenAPI 3 JSON spec with the given service title.
func minSpec(title, version string, paths ...string) []byte {
	pathsJSON := ""
	for _, p := range paths {
		if pathsJSON != "" {
			pathsJSON += ","
		}
		pathsJSON += `"` + p + `":{"get":{"description":"auto"}}`
	}
	return []byte(`{"openapi":"3.0.3","info":{"title":"` + title + `","version":"` + version + `"},"paths":{` + pathsJSON + `}}`)
}
