//go:build integration

package handler_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/kubixhq/kubix-catalog/internal/catalog"
	"github.com/kubixhq/kubix-catalog/internal/config"
	"github.com/kubixhq/kubix-catalog/internal/db"
	"github.com/kubixhq/kubix-catalog/internal/handler"
)

// ── Test setup ────────────────────────────────────────────────────────────────

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
	t.Cleanup(func() {
		_, _ = d.Exec("DELETE FROM catalog_endpoints")
		_, _ = d.Exec("DELETE FROM catalog_dependencies")
		_, _ = d.Exec("DELETE FROM catalog_specs")
		_, _ = d.Exec("DELETE FROM catalog_services")
		d.Close()
	})
	return d
}

func closedDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("postgres", "host=localhost user=nobody dbname=nobody")
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	return d
}

func newMux(d *sql.DB) *http.ServeMux {
	cfg := config.Config{
		ServerPort:          8082,
		SpecFetchTimeoutSec: 5,
		MaxSpecSizeMB:       1,
	}
	store := catalog.NewStore(d)
	h := handler.New(store, cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/catalog/specs", h.IngestSpec)
	mux.HandleFunc("GET /api/catalog/specs/{id}", h.GetSpec)
	mux.HandleFunc("GET /api/catalog/graph", h.Graph)
	mux.HandleFunc("GET /api/catalog/breaking-changes", h.BreakingChanges)
	mux.HandleFunc("GET /api/catalog/services", h.ListServices)
	mux.HandleFunc("GET /api/catalog/services/{id}", h.GetService)
	mux.HandleFunc("DELETE /api/catalog/services/{id}", h.DeleteService)
	mux.HandleFunc("GET /api/catalog/search", h.Search)
	return mux
}

func do(mux *http.ServeMux, method, path string, body []byte) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	mux.ServeHTTP(rr, req)
	return rr
}

func get(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	return do(mux, "GET", path, nil)
}

func post(mux *http.ServeMux, path string, body []byte) *httptest.ResponseRecorder {
	return do(mux, "POST", path, body)
}

func errMsg(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct{ Error string }
	_ = json.NewDecoder(rr.Body).Decode(&body)
	return body.Error
}

// validSpec returns a minimal inline OpenAPI 3 spec JSON for the POST body.
func validSpecBody(serviceName, version string) []byte {
	spec := fmt.Sprintf(`{
    "openapi":"3.0.0",
    "info":{"title":"%s","version":"%s"},
    "paths":{"/health":{"get":{"description":"health check"}}}
  }`, serviceName, version)
	body, _ := json.Marshal(map[string]interface{}{
		"service_name": serviceName,
		"spec_version": version,
		"spec":         json.RawMessage(spec),
	})
	return body
}

// ── POST /api/catalog/specs ───────────────────────────────────────────────────

func TestHandler_IngestSpec_ValidInlineSpec_201(t *testing.T) {
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", validSpecBody("inline-svc", "v1"))
	if rr.Code != http.StatusCreated {
		t.Errorf("got %d, want 201: %s", rr.Code, rr.Body.String())
	}
	var resp catalog.IngestResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ServiceID <= 0 {
		t.Error("ServiceID must be positive")
	}
	if resp.Status != "success" {
		t.Errorf("Status = %q, want success", resp.Status)
	}
	if resp.EndpointCount != 1 {
		t.Errorf("EndpointCount = %d, want 1", resp.EndpointCount)
	}
}

func TestHandler_IngestSpec_ValidURLSpec_201(t *testing.T) {
	specServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"openapi":"3.0.0","info":{"title":"URL Svc","version":"v1"},"paths":{"/ping":{"get":{}}}}`)
	}))
	defer specServer.Close()

	body, _ := json.Marshal(map[string]string{
		"service_name": "url-svc",
		"spec_url":     specServer.URL + "/spec.json",
		"spec_version": "v1",
	})
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusCreated {
		t.Errorf("got %d, want 201: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_IngestSpec_URLReturnsHTML_400(t *testing.T) {
	htmlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>Not an API spec</body></html>")
	}))
	defer htmlServer.Close()

	body, _ := json.Marshal(map[string]string{
		"service_name": "html-svc",
		"spec_url":     htmlServer.URL,
	})
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("HTML URL: got %d, want 400", rr.Code)
	}
}

func TestHandler_IngestSpec_URLUnreachable_503(t *testing.T) {
	body, _ := json.Marshal(map[string]string{
		"service_name": "unreachable-svc",
		"spec_url":     "http://127.0.0.1:19999/nonexistent",
	})
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("unreachable URL: got %d, want 503", rr.Code)
	}
}

func TestHandler_IngestSpec_SpecTooLarge_413(t *testing.T) {
	cfg := config.Config{SpecFetchTimeoutSec: 5, MaxSpecSizeMB: 1}
	store := catalog.NewStore(testDB(t))
	h := handler.New(store, cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/catalog/specs", h.IngestSpec)

	// Build a spec that exceeds 1MB
	bigSpec := make([]byte, 1024*1024+100)
	for i := range bigSpec {
		bigSpec[i] = 'a'
	}
	body, _ := json.Marshal(map[string]interface{}{
		"service_name": "big-svc",
		"spec":         json.RawMessage(`"` + string(bytes.Repeat([]byte("x"), 1024*1024+1)) + `"`),
	})
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("large spec: got %d, want 413", rr.Code)
	}
}

func TestHandler_IngestSpec_MissingServiceName_400(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{
		"spec": json.RawMessage(`{"openapi":"3.0.0"}`),
	})
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing service_name: got %d, want 400", rr.Code)
	}
	if !strings.Contains(errMsg(t, rr), "service_name") {
		t.Errorf("error should mention service_name: %s", rr.Body.String())
	}
}

func TestHandler_IngestSpec_NeitherURLNorSpec_400(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"service_name": "no-spec-svc"})
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no spec/url: got %d, want 400", rr.Code)
	}
}

func TestHandler_IngestSpec_InvalidJSON_400(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{
		"service_name": "bad-json-svc",
		"spec":         json.RawMessage(`{not valid json`),
	})
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON spec: got %d, want 400", rr.Code)
	}
}

func TestHandler_IngestSpec_InvalidYAML_400(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{
		"service_name": "bad-yaml-svc",
		"spec":         json.RawMessage(`":\t:bad yaml\n\t["`),
	})
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid YAML spec: got %d, want 400", rr.Code)
	}
}

func TestHandler_IngestSpec_NotOpenAPIFormat_400(t *testing.T) {
	spec := json.RawMessage(`{"some":"object","without":"openapi_field"}`)
	body, _ := json.Marshal(map[string]interface{}{
		"service_name": "not-openapi-svc",
		"spec":         spec,
	})
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("not-OpenAPI spec: got %d, want 400", rr.Code)
	}
}

func TestHandler_IngestSpec_DuplicateVersion_409(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("dup-svc", "v1"))
	rr := post(mux, "/api/catalog/specs", validSpecBody("dup-svc", "v1"))
	if rr.Code != http.StatusConflict {
		t.Errorf("duplicate version: got %d, want 409", rr.Code)
	}
}

func TestHandler_IngestSpec_SameServiceNewVersion_201(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("versioned-svc", "v1"))
	rr := post(mux, "/api/catalog/specs", validSpecBody("versioned-svc", "v2"))
	if rr.Code != http.StatusCreated {
		t.Errorf("new version: got %d, want 201: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_IngestSpec_Swagger2_Parsed(t *testing.T) {
	spec := json.RawMessage(`{
    "swagger":"2.0",
    "info":{"title":"SW2","version":"v1"},
    "paths":{"/orders":{"get":{"description":"orders"}}}
  }`)
	body, _ := json.Marshal(map[string]interface{}{
		"service_name": "sw2-svc",
		"spec":         spec,
	})
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusCreated {
		t.Errorf("swagger 2: got %d, want 201: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_IngestSpec_URLWithRedirect_Followed(t *testing.T) {
	finalSpec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"openapi":"3.0.0","info":{"title":"T","version":"v1"},"paths":{}}`)
	}))
	defer finalSpec.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, finalSpec.URL, http.StatusMovedPermanently)
	}))
	defer redirectServer.Close()

	body, _ := json.Marshal(map[string]string{
		"service_name": "redirect-svc",
		"spec_url":     redirectServer.URL,
	})
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusCreated {
		t.Errorf("redirect followed: got %d, want 201: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_IngestSpec_DBUnavailable_503(t *testing.T) {
	spec := json.RawMessage(`{"openapi":"3.0.0","info":{"title":"T","version":"v1"},"paths":{}}`)
	body, _ := json.Marshal(map[string]interface{}{
		"service_name": "db-fail-svc",
		"spec":         spec,
	})
	mux := newMux(closedDB(t))
	rr := post(mux, "/api/catalog/specs", body)
	if rr.Code != http.StatusInternalServerError && rr.Code != http.StatusServiceUnavailable {
		t.Errorf("DB unavailable: got %d, want 500 or 503", rr.Code)
	}
}

// ── GET /api/catalog/specs/{id} ───────────────────────────────────────────────

func TestHandler_GetSpec_Existing_200(t *testing.T) {
	d := testDB(t)
	mux := newMux(d)

	rr := post(mux, "/api/catalog/specs", validSpecBody("spec-get-svc", "v1"))
	var ingestResp catalog.IngestResponse
	json.NewDecoder(rr.Body).Decode(&ingestResp)

	rr = get(mux, fmt.Sprintf("/api/catalog/specs/%d", ingestResp.ServiceID))
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_GetSpec_NonExisting_404(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/specs/999999")
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
}

func TestHandler_GetSpec_InvalidID_400(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/specs/not-a-number")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid id: got %d, want 400", rr.Code)
	}
}

// ── GET /api/catalog/graph ────────────────────────────────────────────────────

func TestHandler_Graph_NoServices_EmptyGraph(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/graph")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var g catalog.GraphResponse
	json.NewDecoder(rr.Body).Decode(&g)
	if len(g.Nodes) != 0 {
		t.Errorf("empty DB: nodes = %d, want 0", len(g.Nodes))
	}
}

func TestHandler_Graph_WithServices_ReturnsNodes(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("graph-svc-a", "v1"))
	post(mux, "/api/catalog/specs", validSpecBody("graph-svc-b", "v1"))

	rr := get(mux, "/api/catalog/graph")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
	}
	var g catalog.GraphResponse
	json.NewDecoder(rr.Body).Decode(&g)
	if len(g.Nodes) < 2 {
		t.Errorf("nodes = %d, want at least 2", len(g.Nodes))
	}
}

func TestHandler_Graph_ContentType(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/graph")
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: %q, want application/json", ct)
	}
}

// ── GET /api/catalog/breaking-changes ────────────────────────────────────────

func TestHandler_BreakingChanges_MissingParams_400(t *testing.T) {
	mux := newMux(testDB(t))

	for _, path := range []string{
		"/api/catalog/breaking-changes",
		"/api/catalog/breaking-changes?service=X",
		"/api/catalog/breaking-changes?service=X&from=v1",
		"/api/catalog/breaking-changes?from=v1&to=v2",
	} {
		rr := get(mux, path)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("GET %s: got %d, want 400", path, rr.Code)
		}
	}
}

func TestHandler_BreakingChanges_ServiceNotFound_404(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/breaking-changes?service=ghost-svc&from=v1&to=v2")
	if rr.Code != http.StatusNotFound {
		t.Errorf("ghost service: got %d, want 404", rr.Code)
	}
}

func TestHandler_BreakingChanges_FromVersionNotFound_404(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("bc-svc", "v2"))
	rr := get(mux, "/api/catalog/breaking-changes?service=bc-svc&from=v1&to=v2")
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing from version: got %d, want 404", rr.Code)
	}
}

func TestHandler_BreakingChanges_ToVersionNotFound_404(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("bc-svc2", "v1"))
	rr := get(mux, "/api/catalog/breaking-changes?service=bc-svc2&from=v1&to=v99")
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing to version: got %d, want 404", rr.Code)
	}
}

func TestHandler_BreakingChanges_FromEqualsTo_EmptyResult(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("bc-same-svc", "v1"))
	rr := get(mux, "/api/catalog/breaking-changes?service=bc-same-svc&from=v1&to=v1")
	if rr.Code != http.StatusOK {
		t.Fatalf("from==to: got %d: %s", rr.Code, rr.Body.String())
	}
	var resp catalog.BreakingChangesResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.BreakingChanges) != 0 {
		t.Errorf("from==to should have 0 changes, got %d", len(resp.BreakingChanges))
	}
	if resp.RiskLevel != "none" {
		t.Errorf("risk_level = %q, want none", resp.RiskLevel)
	}
}

func TestHandler_BreakingChanges_ValidVersions_200(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("bc-valid-svc", "v1"))
	post(mux, "/api/catalog/specs", validSpecBody("bc-valid-svc", "v2"))
	rr := get(mux, "/api/catalog/breaking-changes?service=bc-valid-svc&from=v1&to=v2")
	if rr.Code != http.StatusOK {
		t.Errorf("valid versions: got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp catalog.BreakingChangesResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.BreakingChanges == nil {
		t.Error("breaking_changes must not be nil")
	}
	if resp.AffectedServices == nil {
		t.Error("affected_services must not be nil")
	}
}

// ── GET /api/catalog/services ─────────────────────────────────────────────────

func TestHandler_ListServices_EmptyDB_200(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/services")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)
	if _, ok := resp["services"]; !ok {
		t.Error("response must have 'services' field")
	}
}

func TestHandler_ListServices_WithData_200(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("list-svc-1", "v1"))
	post(mux, "/api/catalog/specs", validSpecBody("list-svc-2", "v1"))

	rr := get(mux, "/api/catalog/services")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_ListServices_StatusFilter(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("status-svc", "v1"))

	rr := get(mux, "/api/catalog/services?status=active")
	if rr.Code != http.StatusOK {
		t.Errorf("status=active: got %d, want 200", rr.Code)
	}
}

func TestHandler_ListServices_SearchFilter(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("search-handler-svc", "v1"))

	rr := get(mux, "/api/catalog/services?search=search-handler")
	if rr.Code != http.StatusOK {
		t.Errorf("search: got %d, want 200", rr.Code)
	}
}

func TestHandler_ListServices_SortByName(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/services?sort=name")
	if rr.Code != http.StatusOK {
		t.Errorf("sort=name: got %d, want 200", rr.Code)
	}
}

func TestHandler_ListServices_SortByUpdated(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/services?sort=updated")
	if rr.Code != http.StatusOK {
		t.Errorf("sort=updated: got %d, want 200", rr.Code)
	}
}

// ── GET /api/catalog/services/{id} ───────────────────────────────────────────

func TestHandler_GetService_Existing_200(t *testing.T) {
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", validSpecBody("get-svc", "v1"))
	var ingest catalog.IngestResponse
	json.NewDecoder(rr.Body).Decode(&ingest)

	rr = get(mux, fmt.Sprintf("/api/catalog/services/%d", ingest.ServiceID))
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var svc catalog.ServiceInfo
	json.NewDecoder(rr.Body).Decode(&svc)
	if svc.ServiceName != "get-svc" {
		t.Errorf("ServiceName = %q, want get-svc", svc.ServiceName)
	}
}

func TestHandler_GetService_NonExisting_404(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/services/999999")
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
}

func TestHandler_GetService_InvalidID_400(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/services/abc")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid id: got %d, want 400", rr.Code)
	}
}

// ── DELETE /api/catalog/services/{id} ────────────────────────────────────────

func TestHandler_DeleteService_Existing_204(t *testing.T) {
	mux := newMux(testDB(t))
	rr := post(mux, "/api/catalog/specs", validSpecBody("del-svc", "v1"))
	var ingest catalog.IngestResponse
	json.NewDecoder(rr.Body).Decode(&ingest)

	rr = do(mux, "DELETE", fmt.Sprintf("/api/catalog/services/%d", ingest.ServiceID), nil)
	if rr.Code != http.StatusNoContent {
		t.Errorf("delete existing: got %d, want 204: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_DeleteService_NonExisting_404(t *testing.T) {
	mux := newMux(testDB(t))
	rr := do(mux, "DELETE", "/api/catalog/services/999999", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("delete non-existing: got %d, want 404", rr.Code)
	}
}

func TestHandler_DeleteService_InvalidID_400(t *testing.T) {
	mux := newMux(testDB(t))
	rr := do(mux, "DELETE", "/api/catalog/services/notanid", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid id: got %d, want 400", rr.Code)
	}
}

// ── GET /api/catalog/search ───────────────────────────────────────────────────

func TestHandler_Search_ValidQuery_200(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("search-svc", "v1"))

	rr := get(mux, "/api/catalog/search?q=health")
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)
	if _, ok := resp["results"]; !ok {
		t.Error("response must have 'results' field")
	}
}

func TestHandler_Search_EmptyQ_400(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/search")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("empty q: got %d, want 400", rr.Code)
	}
}

func TestHandler_Search_OneCharQ_400(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/search?q=a")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("1-char q: got %d, want 400", rr.Code)
	}
}

func TestHandler_Search_NoResults_ReturnsEmptyNotError(t *testing.T) {
	mux := newMux(testDB(t))
	rr := get(mux, "/api/catalog/search?q=xxxxxxxxxxxxxxxxx")
	if rr.Code != http.StatusOK {
		t.Errorf("no results: got %d, want 200 (not 404)", rr.Code)
	}
}

func TestHandler_Search_TwoCharMinimum_200(t *testing.T) {
	mux := newMux(testDB(t))
	post(mux, "/api/catalog/specs", validSpecBody("two-char-svc", "v1"))
	rr := get(mux, "/api/catalog/search?q=he")
	if rr.Code != http.StatusOK {
		t.Errorf("2-char q: got %d, want 200", rr.Code)
	}
}

// ── Method routing ────────────────────────────────────────────────────────────

func TestHandler_WrongMethod_PostOnGetEndpoint_405(t *testing.T) {
	mux := newMux(closedDB(t))
	for _, path := range []string{
		"/api/catalog/graph",
		"/api/catalog/services",
		"/api/catalog/search?q=test",
	} {
		rr := post(mux, path, nil)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: got %d, want 405", path, rr.Code)
		}
	}
}

func TestHandler_WrongMethod_GetOnPostEndpoint_405(t *testing.T) {
	mux := newMux(closedDB(t))
	rr := get(mux, "/api/catalog/specs")
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/catalog/specs: got %d, want 405", rr.Code)
	}
}

func TestHandler_UnknownEndpoint_404(t *testing.T) {
	mux := newMux(closedDB(t))
	for _, path := range []string{"/", "/api", "/api/catalog", "/api/catalog/unknown"} {
		rr := get(mux, path)
		if rr.Code != http.StatusNotFound {
			t.Errorf("GET %s: got %d, want 404", path, rr.Code)
		}
	}
}

// ── Response format ───────────────────────────────────────────────────────────

func TestHandler_AllEndpoints_ReturnJSON(t *testing.T) {
	d := testDB(t)
	mux := newMux(d)
	post(mux, "/api/catalog/specs", validSpecBody("ct-svc", "v1"))

	paths := []string{
		"/api/catalog/graph",
		"/api/catalog/services",
		"/api/catalog/search?q=health",
	}
	for _, path := range paths {
		rr := get(mux, path)
		ct := rr.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			t.Errorf("GET %s: Content-Type = %q, want application/json", path, ct)
		}
	}
}

func TestHandler_ErrorResponses_HaveErrorField(t *testing.T) {
	mux := newMux(closedDB(t))
	for _, tc := range []struct {
		method, path string
		body         []byte
	}{
		{"GET", "/api/catalog/search", nil},
		{"GET", "/api/catalog/services/abc", nil},
		{"POST", "/api/catalog/specs", []byte(`{"service_name":""}`)},
	} {
		rr := do(mux, tc.method, tc.path, tc.body)
		if rr.Code >= 400 {
			msg := errMsg(t, rr)
			if msg == "" {
				t.Errorf("%s %s status=%d: error field empty in response", tc.method, tc.path, rr.Code)
			}
		}
	}
}

// ── Config validation ─────────────────────────────────────────────────────────

func TestConfig_Validate_Valid(t *testing.T) {
	cfg := config.Config{DBPort: 5432, ServerPort: 8082, SpecFetchTimeoutSec: 10, MaxSpecSizeMB: 10}
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid config: unexpected error: %v", err)
	}
}

func TestConfig_Validate_DBPort_Zero(t *testing.T) {
	cfg := config.Config{DBPort: 0, ServerPort: 8082, SpecFetchTimeoutSec: 10, MaxSpecSizeMB: 10}
	if err := cfg.Validate(); err == nil {
		t.Error("DBPort=0 should fail validation")
	}
}

func TestConfig_Validate_DBPort_OutOfRange(t *testing.T) {
	cfg := config.Config{DBPort: 99999, ServerPort: 8082, SpecFetchTimeoutSec: 10, MaxSpecSizeMB: 10}
	if err := cfg.Validate(); err == nil {
		t.Error("DBPort=99999 should fail validation")
	}
}

func TestConfig_Validate_NegativeTimeout(t *testing.T) {
	cfg := config.Config{DBPort: 5432, ServerPort: 8082, SpecFetchTimeoutSec: -1, MaxSpecSizeMB: 10}
	if err := cfg.Validate(); err == nil {
		t.Error("negative SPEC_FETCH_TIMEOUT_SEC should fail")
	}
}

func TestConfig_Validate_ZeroMaxSize(t *testing.T) {
	cfg := config.Config{DBPort: 5432, ServerPort: 8082, SpecFetchTimeoutSec: 10, MaxSpecSizeMB: 0}
	if err := cfg.Validate(); err == nil {
		t.Error("MAX_SPEC_SIZE_MB=0 should fail")
	}
}

func TestConfig_Validate_ServerPort_Zero(t *testing.T) {
	cfg := config.Config{DBPort: 5432, ServerPort: 0, SpecFetchTimeoutSec: 10, MaxSpecSizeMB: 10}
	if err := cfg.Validate(); err == nil {
		t.Error("SERVER_PORT=0 should fail")
	}
}
