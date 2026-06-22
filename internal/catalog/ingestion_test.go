//go:build integration

package catalog_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kubixhq/kubix-catalog/internal/catalog"
)

// ── Parser: format detection ─────────────────────────────────────────────────

var openAPI3JSON = []byte(`{
  "openapi": "3.0.3",
  "info": {"title": "User Service", "version": "v1"},
  "x-kubix-calls": ["payment-service", "notification-service"],
  "paths": {
    "/users": {
      "get": {
        "description": "List users",
        "tags": ["users"],
        "parameters": [
          {"name": "limit", "in": "query", "required": false, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {"users": {"type": "array"}}
                }
              }
            }
          }
        }
      },
      "post": {
        "description": "Create user",
        "tags": ["users"],
        "requestBody": {
          "required": true,
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "required": ["email"],
                "properties": {
                  "email": {"type": "string"},
                  "name":  {"type": "string"}
                }
              }
            }
          }
        },
        "responses": {"201": {"description": "Created"}}
      }
    },
    "/users/{id}": {
      "delete": {
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}},
          {"name": "Authorization", "in": "header", "required": true, "schema": {"type": "string"}}
        ],
        "responses": {"204": {"description": "Deleted"}}
      }
    }
  }
}`)

var swagger2JSON = []byte(`{
  "swagger": "2.0",
  "info": {"title": "Order Service", "version": "v2"},
  "x-kubix-calls": ["inventory-service"],
  "paths": {
    "/orders": {
      "post": {
        "description": "Create order",
        "tags": ["orders"],
        "parameters": [{
          "name": "body", "in": "body", "required": true,
          "schema": {
            "type": "object",
            "required": ["product_id"],
            "properties": {
              "product_id": {"type": "string"},
              "quantity":   {"type": "integer"}
            }
          }
        }],
        "responses": {
          "201": {
            "description": "Created",
            "schema": {
              "type": "object",
              "properties": {"id": {"type": "string"}, "status": {"type": "string"}}
            }
          }
        }
      }
    }
  }
}`)

var openAPI3YAML = []byte(`
openapi: "3.0.3"
info:
  title: Auth Service
  version: v3
paths:
  /login:
    post:
      description: Login
      tags:
        - auth
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required:
                - username
              properties:
                username:
                  type: string
                password:
                  type: string
      responses:
        "200":
          description: OK
`)

var swagger2YAML = []byte(`
swagger: "2.0"
info:
  title: Profile Service
  version: v4
paths:
  /profile:
    get:
      description: Get profile
      tags:
        - profile
      parameters:
        - name: id
          in: query
          type: string
      responses:
        200:
          description: OK
          schema:
            type: object
            properties:
              id:
                type: string
              name:
                type: string
`)

func TestParse_OpenAPI3_JSON(t *testing.T) {
	ps, err := catalog.Parse(openAPI3JSON)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ps.APIVersion != "v1" {
		t.Errorf("APIVersion = %q, want v1", ps.APIVersion)
	}
	if ps.Title != "User Service" {
		t.Errorf("Title = %q, want User Service", ps.Title)
	}
	if len(ps.Endpoints) != 3 {
		t.Errorf("endpoint count = %d, want 3", len(ps.Endpoints))
	}
	if len(ps.Calls) != 2 {
		t.Errorf("Calls count = %d, want 2", len(ps.Calls))
	}
	if ps.RawJSON == nil {
		t.Error("RawJSON must not be nil")
	}
}

func TestParse_OpenAPI3_Tags_And_Description(t *testing.T) {
	ps, err := catalog.Parse(openAPI3JSON)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var getUsers *catalog.Endpoint
	for i := range ps.Endpoints {
		if ps.Endpoints[i].Method == "GET" && ps.Endpoints[i].Path == "/users" {
			getUsers = &ps.Endpoints[i]
			break
		}
	}
	if getUsers == nil {
		t.Fatal("GET /users not found in parsed endpoints")
	}
	if getUsers.Description != "List users" {
		t.Errorf("Description = %q, want 'List users'", getUsers.Description)
	}
	if len(getUsers.Tags) != 1 || getUsers.Tags[0] != "users" {
		t.Errorf("Tags = %v, want [users]", getUsers.Tags)
	}
}

func TestParse_OpenAPI3_Parameters(t *testing.T) {
	ps, err := catalog.Parse(openAPI3JSON)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var deleteUser *catalog.Endpoint
	for i := range ps.Endpoints {
		if ps.Endpoints[i].Method == "DELETE" {
			deleteUser = &ps.Endpoints[i]
			break
		}
	}
	if deleteUser == nil {
		t.Fatal("DELETE /users/{id} not found")
	}
	if len(deleteUser.Parameters) != 2 {
		t.Errorf("parameter count = %d, want 2", len(deleteUser.Parameters))
	}
}

func TestParse_OpenAPI3_RequestBody(t *testing.T) {
	ps, err := catalog.Parse(openAPI3JSON)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var post *catalog.Endpoint
	for i := range ps.Endpoints {
		if ps.Endpoints[i].Method == "POST" && ps.Endpoints[i].Path == "/users" {
			post = &ps.Endpoints[i]
			break
		}
	}
	if post == nil {
		t.Fatal("POST /users not found")
	}
	if post.RequestBody == nil {
		t.Fatal("RequestBody must not be nil")
	}
	if !post.RequestBody.BodyRequired {
		t.Error("RequestBody.BodyRequired must be true")
	}
	if len(post.RequestBody.RequiredFields) != 1 || post.RequestBody.RequiredFields[0] != "email" {
		t.Errorf("RequiredFields = %v, want [email]", post.RequestBody.RequiredFields)
	}
	if _, ok := post.RequestBody.Properties["email"]; !ok {
		t.Error("Properties must contain 'email'")
	}
}

func TestParse_Swagger2_JSON(t *testing.T) {
	ps, err := catalog.Parse(swagger2JSON)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ps.APIVersion != "v2" {
		t.Errorf("APIVersion = %q, want v2", ps.APIVersion)
	}
	if len(ps.Endpoints) != 1 {
		t.Errorf("endpoint count = %d, want 1", len(ps.Endpoints))
	}
	if ps.Calls[0] != "inventory-service" {
		t.Errorf("Calls = %v, want [inventory-service]", ps.Calls)
	}
}

func TestParse_Swagger2_RequestBody_FromBodyParam(t *testing.T) {
	ps, err := catalog.Parse(swagger2JSON)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ep := ps.Endpoints[0]
	if ep.RequestBody == nil {
		t.Fatal("RequestBody must not be nil for swagger2 body param")
	}
	if len(ep.RequestBody.RequiredFields) != 1 || ep.RequestBody.RequiredFields[0] != "product_id" {
		t.Errorf("RequiredFields = %v, want [product_id]", ep.RequestBody.RequiredFields)
	}
}

func TestParse_OpenAPI3_YAML(t *testing.T) {
	ps, err := catalog.Parse(openAPI3YAML)
	if err != nil {
		t.Fatalf("Parse YAML: %v", err)
	}
	if ps.Title != "Auth Service" {
		t.Errorf("Title = %q", ps.Title)
	}
	if len(ps.Endpoints) != 1 {
		t.Errorf("endpoint count = %d, want 1", len(ps.Endpoints))
	}
}

func TestParse_Swagger2_YAML(t *testing.T) {
	ps, err := catalog.Parse(swagger2YAML)
	if err != nil {
		t.Fatalf("Parse YAML: %v", err)
	}
	if ps.Title != "Profile Service" {
		t.Errorf("Title = %q", ps.Title)
	}
	if len(ps.Endpoints) != 1 {
		t.Errorf("endpoint count = %d, want 1", len(ps.Endpoints))
	}
}

func TestParse_EmptyPaths_ZeroEndpoints(t *testing.T) {
	spec := []byte(`{"openapi":"3.0.0","info":{"title":"Empty","version":"v1"},"paths":{}}`)
	ps, err := catalog.Parse(spec)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(ps.Endpoints) != 0 {
		t.Errorf("expected 0 endpoints, got %d", len(ps.Endpoints))
	}
}

func TestParse_100PlusEndpoints(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"openapi":"3.0.0","info":{"title":"Big","version":"v1"},"paths":{`)
	for i := 0; i < 120; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `"/resource/%d":{"get":{"description":"item %d"}}`, i, i)
	}
	sb.WriteString(`}}`)
	ps, err := catalog.Parse([]byte(sb.String()))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(ps.Endpoints) != 120 {
		t.Errorf("endpoint count = %d, want 120", len(ps.Endpoints))
	}
}

func TestParse_SecurityScheme_DoesNotFail(t *testing.T) {
	spec := []byte(`{
    "openapi":"3.0.0","info":{"title":"Secure","version":"v1"},
    "components":{"securitySchemes":{"bearerAuth":{"type":"http","scheme":"bearer"}}},
    "paths":{"/ping":{"get":{"security":[{"bearerAuth":[]}],"responses":{"200":{"description":"OK"}}}}}
  }`)
	_, err := catalog.Parse(spec)
	if err != nil {
		t.Fatalf("Parse with security scheme should not fail: %v", err)
	}
}

func TestParse_InvalidJSON_ReturnsError(t *testing.T) {
	_, err := catalog.Parse([]byte(`{not valid json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParse_InvalidYAML_ReturnsError(t *testing.T) {
	_, err := catalog.Parse([]byte(":\t:bad yaml\n\t["))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestParse_NotOpenAPIOrSwagger_ReturnsError(t *testing.T) {
	_, err := catalog.Parse([]byte(`{"some":"other","json":"object"}`))
	if err == nil {
		t.Error("expected error for non-OpenAPI document")
	}
}

func TestParse_HTMLContent_ReturnsError(t *testing.T) {
	_, err := catalog.Parse([]byte(`<!DOCTYPE html><html><body>Not an API spec</body></html>`))
	if err == nil {
		t.Error("expected error for HTML content")
	}
}

func TestParse_XKubixCalls_Extracted(t *testing.T) {
	ps, err := catalog.Parse(openAPI3JSON)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(ps.Calls) == 0 {
		t.Fatal("x-kubix-calls should be extracted")
	}
}

func TestParse_NoXKubixCalls_EmptySlice(t *testing.T) {
	spec := []byte(`{"openapi":"3.0.0","info":{"title":"T","version":"v1"},"paths":{}}`)
	ps, err := catalog.Parse(spec)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(ps.Calls) != 0 {
		t.Errorf("expected empty calls, got %v", ps.Calls)
	}
}

// ── Store: ingestion ─────────────────────────────────────────────────────────

func TestStore_IngestSpec_NewService(t *testing.T) {
	s, _ := newStore(t)
	id, err := s.IngestSpec("svc-new", "http://example.com/spec", "v1",
		[]catalog.Endpoint{ep("GET", "/health")}, []byte("{}"))
	if err != nil {
		t.Fatalf("IngestSpec: %v", err)
	}
	if id <= 0 {
		t.Errorf("serviceID = %d, want > 0", id)
	}
}

func TestStore_IngestSpec_ReturnsCorrectEndpointCount(t *testing.T) {
	s, _ := newStore(t)
	endpoints := []catalog.Endpoint{
		ep("GET", "/a"), ep("POST", "/b"), ep("DELETE", "/c"),
	}
	mustIngest(t, s, "svc-count", "v1", endpoints)

	svc, err := s.GetService(mustIngest(t, s, "svc-count2", "v1", endpoints))
	if err != nil {
		t.Fatalf("GetService: %v", err)
	}
	if svc.EndpointCount != 3 {
		t.Errorf("EndpointCount = %d, want 3", svc.EndpointCount)
	}
}

func TestStore_IngestSpec_SameServiceNewVersion_Allowed(t *testing.T) {
	s, _ := newStore(t)
	id1 := mustIngest(t, s, "svc-versioned", "v1", []catalog.Endpoint{ep("GET", "/v1")})
	id2, err := s.IngestSpec("svc-versioned", "", "v2", []catalog.Endpoint{ep("GET", "/v2")}, []byte("{}"))
	if err != nil {
		t.Fatalf("second version IngestSpec: %v", err)
	}
	if id1 != id2 {
		t.Error("same service should keep the same service ID")
	}
}

func TestStore_IngestSpec_DuplicateVersion_Returns409(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "svc-dup", "v1", nil)
	_, err := s.IngestSpec("svc-dup", "", "v1", nil, []byte("{}"))
	if err == nil {
		t.Fatal("expected ConflictError for duplicate (service, version)")
	}
	if _, ok := err.(*catalog.ConflictError); !ok {
		t.Errorf("want ConflictError, got %T: %v", err, err)
	}
}

func TestStore_IngestSpec_GetSpecVersion_RoundTrip(t *testing.T) {
	s, _ := newStore(t)
	want := []catalog.Endpoint{
		{Method: "GET", Path: "/users", Description: "list", Tags: []string{"users"}},
	}
	mustIngest(t, s, "svc-rt", "v1", want)
	got, err := s.GetSpecVersion("svc-rt", "v1")
	if err != nil {
		t.Fatalf("GetSpecVersion: %v", err)
	}
	if len(got) != 1 || got[0].Method != "GET" || got[0].Path != "/users" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestStore_IngestSpec_100Endpoints(t *testing.T) {
	s, _ := newStore(t)
	endpoints := make([]catalog.Endpoint, 100)
	for i := range endpoints {
		endpoints[i] = ep("GET", fmt.Sprintf("/resource/%d", i))
	}
	id := mustIngest(t, s, "svc-big", "v1", endpoints)
	svc, err := s.GetService(id)
	if err != nil {
		t.Fatalf("GetService: %v", err)
	}
	if svc.EndpointCount != 100 {
		t.Errorf("EndpointCount = %d, want 100", svc.EndpointCount)
	}
}

func TestStore_IngestSpec_Dependencies_Saved(t *testing.T) {
	s, _ := newStore(t)
	mustIngest(t, s, "svc-from", "v1", nil)
	if err := s.SaveDependencies("svc-from", []string{"svc-to"}); err != nil {
		t.Fatalf("SaveDependencies: %v", err)
	}
	deps, err := s.AllDependencies()
	if err != nil {
		t.Fatalf("AllDependencies: %v", err)
	}
	if len(deps) == 0 {
		t.Error("expected at least 1 dependency")
	}
}

// TestStore_ServiceName255Plus_Rejected verifies the DB rejects overlong names.
func TestStore_ServiceName255Plus_Rejected(t *testing.T) {
	s, _ := newStore(t)
	longName := strings.Repeat("a", 256)
	_, err := s.IngestSpec(longName, "", "v1", nil, []byte("{}"))
	if err == nil {
		t.Error("expected error for service_name > 255 chars")
	}
}
