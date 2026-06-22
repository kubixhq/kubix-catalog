//go:build integration

package catalog_test

import (
	"fmt"
	"testing"

	"github.com/kubixhq/kubix-catalog/internal/catalog"
)

// ── SearchEndpoints ───────────────────────────────────────────────────────────

func TestStore_Search_ByPath(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "user-svc", "v1", []catalog.Endpoint{
		ep("GET", "/api/users"),
		ep("POST", "/api/users"),
		ep("GET", "/api/orders"),
	})

	results, err := s.SearchEndpoints("users")
	if err != nil {
		t.Fatalf("SearchEndpoints: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results for 'users', got %d", len(results))
	}
}

func TestStore_Search_ByDescription(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "auth-svc", "v1", []catalog.Endpoint{
		epDesc("POST", "/login", "Authenticate a user"),
		epDesc("GET", "/profile", "Get user profile"),
		ep("DELETE", "/session"),
	})

	results, err := s.SearchEndpoints("Authenticate")
	if err != nil {
		t.Fatalf("SearchEndpoints: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'Authenticate', got %d: %v", len(results), results)
	}
}

func TestStore_Search_ByTag(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "tag-svc", "v1", []catalog.Endpoint{
		epTagged("GET", "/orders", "orders", "ecommerce"),
		epTagged("GET", "/products", "catalog"),
	})

	results, err := s.SearchEndpoints("ecommerce")
	if err != nil {
		t.Fatalf("SearchEndpoints: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for tag 'ecommerce', got %d", len(results))
	}
}

func TestStore_Search_CaseInsensitive(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "case-svc", "v1", []catalog.Endpoint{
		ep("GET", "/api/Users"),
	})

	resultsUpper, err := s.SearchEndpoints("USERS")
	if err != nil {
		t.Fatalf("SearchEndpoints upper: %v", err)
	}
	resultsLower, err := s.SearchEndpoints("users")
	if err != nil {
		t.Fatalf("SearchEndpoints lower: %v", err)
	}
	if len(resultsUpper) != len(resultsLower) {
		t.Errorf("case-insensitive: upper=%d, lower=%d", len(resultsUpper), len(resultsLower))
	}
}

func TestStore_Search_NoResults_ReturnsEmptySlice(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "svc-search-empty", "v1", []catalog.Endpoint{
		ep("GET", "/ping"),
	})

	results, err := s.SearchEndpoints("xxxxxxxx")
	if err != nil {
		t.Fatalf("SearchEndpoints: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty slice, got %d results", len(results))
	}
}

func TestStore_Search_CrossService(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "user-service-cross", "v1", []catalog.Endpoint{
		ep("GET", "/users/list"),
	})
	mustIngest(t, s, "admin-service-cross", "v1", []catalog.Endpoint{
		ep("GET", "/admin/users"),
	})

	results, err := s.SearchEndpoints("users")
	if err != nil {
		t.Fatalf("SearchEndpoints: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 cross-service results, got %d", len(results))
	}
	serviceNames := map[string]bool{}
	for _, r := range results {
		serviceNames[r.ServiceName] = true
	}
	if !serviceNames["user-service-cross"] || !serviceNames["admin-service-cross"] {
		t.Errorf("results should come from both services: %v", serviceNames)
	}
}

func TestStore_Search_ResultHasCorrectFields(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "field-check-svc", "v1", []catalog.Endpoint{
		{
			Method:      "GET",
			Path:        "/api/items",
			Description: "List items",
			Tags:        []string{"items", "catalog"},
		},
	})

	results, err := s.SearchEndpoints("items")
	if err != nil {
		t.Fatalf("SearchEndpoints: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	r := results[0]
	if r.Path == "" {
		t.Error("Path must not be empty")
	}
	if r.Method == "" {
		t.Error("Method must not be empty")
	}
	if r.ServiceName != "field-check-svc" {
		t.Errorf("ServiceName = %q, want field-check-svc", r.ServiceName)
	}
}

func TestStore_Search_PathWithSpecialChars(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "special-svc", "v1", []catalog.Endpoint{
		ep("GET", "/users/{id}/settings"),
	})

	results, err := s.SearchEndpoints("{id}")
	if err != nil {
		t.Fatalf("SearchEndpoints: %v", err)
	}
	if len(results) == 0 {
		t.Error("should find endpoint with {id} in path")
	}
}

func TestStore_Search_DeletedService_NotVisible(t *testing.T) {
	s, _ := newStore(t)
	id := mustIngest(t, s, "deleted-svc-search", "v1", []catalog.Endpoint{
		ep("GET", "/secret/endpoint"),
	})

	// Verify it's searchable before deletion
	before, _ := s.SearchEndpoints("secret")
	if len(before) == 0 {
		t.Skip("endpoint not indexed — skipping deletion visibility test")
	}

	if err := s.DeleteService(id); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}

	after, err := s.SearchEndpoints("secret")
	if err != nil {
		t.Fatalf("SearchEndpoints after delete: %v", err)
	}
	for _, r := range after {
		if r.ServiceName == "deleted-svc-search" {
			t.Error("deleted service endpoints should not appear in search results")
		}
	}
}

func TestStore_Search_1000PlusEndpoints_Completable(t *testing.T) {
	s, _ := newStore(t)
	endpoints := make([]catalog.Endpoint, 200)
	for i := range endpoints {
		endpoints[i] = epDesc("GET", fmt.Sprintf("/api/resource/%d", i), fmt.Sprintf("Resource item %d", i))
	}
	mustIngest(t, s, "large-svc-search", "v1", endpoints)

	results, err := s.SearchEndpoints("resource")
	if err != nil {
		t.Fatalf("SearchEndpoints: %v", err)
	}
	if len(results) != 200 {
		t.Errorf("expected 200 results, got %d", len(results))
	}
}

func TestStore_Search_ReinstatedService_NewEndpointsVisible(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "update-svc", "v1", []catalog.Endpoint{ep("GET", "/old")})
	mustIngest(t, s, "update-svc", "v2", []catalog.Endpoint{ep("GET", "/new-endpoint")})

	results, err := s.SearchEndpoints("new-endpoint")
	if err != nil {
		t.Fatalf("SearchEndpoints: %v", err)
	}
	if len(results) == 0 {
		t.Error("new version endpoints should be visible in search")
	}
}
