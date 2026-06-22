//go:build integration

package catalog_test

import (
	"fmt"
	"testing"

	"github.com/kubixhq/kubix-catalog/internal/catalog"
)

// ── ListServices ──────────────────────────────────────────────────────────────

func TestStore_ListServices_EmptyDB_ReturnsEmptySlice(t *testing.T) {
	s, _ := newStore(t)
	svcs, err := s.ListServices("", "", "")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(svcs) != 0 {
		t.Errorf("expected empty slice, got %d services", len(svcs))
	}
}

func TestStore_ListServices_ReturnsAll(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "alpha", "v1", nil)
	mustIngest(t, s, "beta", "v1", nil)
	mustIngest(t, s, "gamma", "v1", nil)

	svcs, err := s.ListServices("", "", "")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(svcs) != 3 {
		t.Errorf("expected 3 services, got %d", len(svcs))
	}
}

func TestStore_ListServices_FilterByStatus_Active(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "svc-active", "v1", nil)

	svcs, err := s.ListServices("active", "", "")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	for _, svc := range svcs {
		if svc.Health != "active" {
			t.Errorf("expected health=active, got %q for %q", svc.Health, svc.ServiceName)
		}
	}
}

func TestStore_ListServices_FilterByStatus_Inactive_EmptyByDefault(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "svc-for-inactive-check", "v1", nil)

	svcs, err := s.ListServices("inactive", "", "")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	// Freshly ingested services default to 'active', so none should match
	for _, svc := range svcs {
		if svc.ServiceName == "svc-for-inactive-check" {
			t.Error("active service should not appear in inactive filter")
		}
	}
}

func TestStore_ListServices_SearchByName_CaseInsensitive(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "user-service", "v1", nil)
	mustIngest(t, s, "order-service", "v1", nil)

	svcs, err := s.ListServices("", "USER", "")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(svcs) != 1 {
		t.Errorf("search USER: got %d results, want 1", len(svcs))
	}
	if svcs[0].ServiceName != "user-service" {
		t.Errorf("unexpected service: %q", svcs[0].ServiceName)
	}
}

func TestStore_ListServices_SearchNotFound_EmptySlice(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "alpha-svc", "v1", nil)

	svcs, err := s.ListServices("", "xxxxxxxxxx", "")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(svcs) != 0 {
		t.Errorf("no-match search: expected 0, got %d", len(svcs))
	}
}

func TestStore_ListServices_SearchEmpty_ReturnsAll(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "svc1", "v1", nil)
	mustIngest(t, s, "svc2", "v1", nil)

	all, err := s.ListServices("", "", "")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	emptySearch, err := s.ListServices("", "", "")
	if err != nil {
		t.Fatalf("ListServices empty search: %v", err)
	}
	if len(all) != len(emptySearch) {
		t.Error("empty search should return same results as no search")
	}
}

func TestStore_ListServices_SortByName(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "zebra-svc", "v1", nil)
	mustIngest(t, s, "alpha-svc", "v1", nil)
	mustIngest(t, s, "mango-svc", "v1", nil)

	svcs, err := s.ListServices("", "", "name")
	if err != nil {
		t.Fatalf("ListServices sort=name: %v", err)
	}
	if len(svcs) < 3 {
		t.Fatalf("expected at least 3 services, got %d", len(svcs))
	}
	// Find our services and verify order
	prev := ""
	for _, svc := range svcs {
		if svc.ServiceName < prev {
			t.Errorf("services not in name order: %q comes after %q", svc.ServiceName, prev)
		}
		prev = svc.ServiceName
	}
}

func TestStore_ListServices_SortByUpdated(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "first-svc", "v1", nil)
	mustIngest(t, s, "second-svc", "v1", nil)

	svcs, err := s.ListServices("", "", "updated")
	if err != nil {
		t.Fatalf("ListServices sort=updated: %v", err)
	}
	// Verify descending order (most recently updated first)
	for i := 1; i < len(svcs); i++ {
		if svcs[i].LastUpdated.After(svcs[i-1].LastUpdated) {
			t.Errorf("services not sorted by updated DESC at index %d", i)
		}
	}
}

func TestStore_ListServices_100Services_ReturnsAll(t *testing.T) {
	s, _ := newStore(t)
	for i := 0; i < 100; i++ {
		mustIngest(t, s, fmt.Sprintf("bulk-svc-%03d", i), "v1", nil)
	}
	svcs, err := s.ListServices("", "", "")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(svcs) < 100 {
		t.Errorf("expected at least 100 services, got %d", len(svcs))
	}
}

func TestStore_ListServices_SpecialCharsInName_ReturnedCorrectly(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "my-service_v2.0", "v1", nil)

	svcs, err := s.ListServices("", "my-service_v2", "")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(svcs) != 1 {
		t.Errorf("expected 1, got %d", len(svcs))
	}
}

// ── GetService ────────────────────────────────────────────────────────────────

func TestStore_GetService_ExistingID_ReturnsService(t *testing.T) {
	s, _ := newStore(t)
	id := mustIngest(t, s, "get-me", "v1", []catalog.Endpoint{ep("GET", "/ping")})

	svc, err := s.GetService(id)
	if err != nil {
		t.Fatalf("GetService: %v", err)
	}
	if svc.ServiceName != "get-me" {
		t.Errorf("ServiceName = %q, want get-me", svc.ServiceName)
	}
	if svc.EndpointCount != 1 {
		t.Errorf("EndpointCount = %d, want 1", svc.EndpointCount)
	}
	if svc.Health != "active" {
		t.Errorf("Health = %q, want active", svc.Health)
	}
}

func TestStore_GetService_NonExistingID_NotFound(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.GetService(999999)
	if err == nil {
		t.Fatal("expected NotFoundError")
	}
	if _, ok := err.(*catalog.NotFoundError); !ok {
		t.Errorf("want NotFoundError, got %T: %v", err, err)
	}
}

// ── DeleteService ─────────────────────────────────────────────────────────────

func TestStore_DeleteService_Existing_Succeeds(t *testing.T) {
	s, _ := newStore(t)
	id := mustIngest(t, s, "to-delete", "v1", nil)

	if err := s.DeleteService(id); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}
	_, err := s.GetService(id)
	if err == nil {
		t.Error("service should not exist after deletion")
	}
}

func TestStore_DeleteService_NonExisting_NotFound(t *testing.T) {
	s, _ := newStore(t)
	err := s.DeleteService(999999)
	if err == nil {
		t.Fatal("expected NotFoundError")
	}
	if _, ok := err.(*catalog.NotFoundError); !ok {
		t.Errorf("want NotFoundError, got %T: %v", err, err)
	}
}

func TestStore_DeleteService_CascadesSpecs(t *testing.T) {
	s, _ := newStore(t)
	id := mustIngest(t, s, "cascade-svc", "v1", []catalog.Endpoint{ep("GET", "/x")})

	if err := s.DeleteService(id); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}
	// GetSpecVersion should return NotFoundError
	_, err := s.GetSpecVersion("cascade-svc", "v1")
	if err == nil {
		t.Error("spec should be deleted via cascade")
	}
}

// ── GetSpec ───────────────────────────────────────────────────────────────────

func TestStore_GetSpec_ReturnsLatest(t *testing.T) {
	s, _ := newStore(t)
	id := mustIngest(t, s, "latest-svc", "v1", []catalog.Endpoint{ep("GET", "/v1")})
	mustIngest(t, s, "latest-svc", "v2", []catalog.Endpoint{ep("GET", "/v1"), ep("GET", "/v2")})

	rec, err := s.GetSpec(id)
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	// Latest (v2) has 2 endpoints
	if rec.EndpointCount != 2 {
		t.Errorf("latest spec EndpointCount = %d, want 2", rec.EndpointCount)
	}
}

func TestStore_GetSpec_NonExistingService_NotFound(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.GetSpec(999999)
	if err == nil {
		t.Fatal("expected NotFoundError")
	}
}

// ── GetSpecVersion ────────────────────────────────────────────────────────────

func TestStore_GetSpecVersion_NotFound_ReturnsError(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "version-svc", "v1", nil)

	_, err := s.GetSpecVersion("version-svc", "v99")
	if err == nil {
		t.Fatal("expected NotFoundError for missing version")
	}
}

func TestStore_GetSpecVersion_ServiceNotFound_ReturnsError(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.GetSpecVersion("ghost-service", "v1")
	if err == nil {
		t.Fatal("expected NotFoundError for ghost service")
	}
}
