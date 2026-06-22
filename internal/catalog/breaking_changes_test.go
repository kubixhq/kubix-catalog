//go:build integration

package catalog_test

import (
	"fmt"
	"testing"

	"github.com/kubixhq/kubix-catalog/internal/catalog"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func findChange(changes []catalog.BreakingChange, changeType string) *catalog.BreakingChange {
	for i := range changes {
		if changes[i].Type == changeType {
			return &changes[i]
		}
	}
	return nil
}

func hasChangeType(changes []catalog.BreakingChange, changeType string) bool {
	return findChange(changes, changeType) != nil
}

func assertRisk(t *testing.T, changes []catalog.BreakingChange, changeType, wantRisk string) {
	t.Helper()
	c := findChange(changes, changeType)
	if c == nil {
		t.Errorf("change type %q not found", changeType)
		return
	}
	if c.Risk != wantRisk {
		t.Errorf("change %q: risk = %q, want %q", changeType, c.Risk, wantRisk)
	}
}

func schemaType(typ string) catalog.Schema { return catalog.Schema{Type: typ} }

// ── No changes ────────────────────────────────────────────────────────────────

func TestBreaking_IdenticalSpecs_NoChanges(t *testing.T) {
	endpoints := []catalog.Endpoint{ep("GET", "/users"), ep("POST", "/orders")}
	changes := catalog.DetectBreakingChanges(endpoints, endpoints)
	if len(changes) != 0 {
		t.Errorf("identical specs: expected 0 changes, got %d: %v", len(changes), changes)
	}
}

func TestBreaking_RiskLevel_None(t *testing.T) {
	if catalog.RiskLevel(nil) != "none" {
		t.Error("nil changes should be 'none'")
	}
	if catalog.RiskLevel([]catalog.BreakingChange{}) != "none" {
		t.Error("empty changes should be 'none'")
	}
}

// ── Endpoint level ────────────────────────────────────────────────────────────

func TestBreaking_EndpointRemoved_HIGH(t *testing.T) {
	old := []catalog.Endpoint{ep("GET", "/users"), ep("DELETE", "/users/{id}")}
	new := []catalog.Endpoint{ep("GET", "/users")}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "endpoint_removed", "HIGH")
}

func TestBreaking_EndpointAdded_NotBreaking(t *testing.T) {
	old := []catalog.Endpoint{ep("GET", "/users")}
	new := []catalog.Endpoint{ep("GET", "/users"), ep("POST", "/users")}
	changes := catalog.DetectBreakingChanges(old, new)
	if hasChangeType(changes, "endpoint_added") {
		t.Error("adding an endpoint is not a breaking change")
	}
}

func TestBreaking_MethodChanged_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/search"}}
	new := []catalog.Endpoint{{Method: "POST", Path: "/search"}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "method_changed", "HIGH")
}

// ── Request: parameters ───────────────────────────────────────────────────────

func TestBreaking_RequiredParamAdded_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/users"}}
	new := []catalog.Endpoint{{
		Method: "GET", Path: "/users",
		Parameters: []catalog.Parameter{{Name: "filter", In: "query", Required: true, Schema: schemaType("string")}},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "required_parameter_added", "HIGH")
}

func TestBreaking_OptionalParamAdded_NotBreaking(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/users"}}
	new := []catalog.Endpoint{{
		Method: "GET", Path: "/users",
		Parameters: []catalog.Parameter{{Name: "sort", In: "query", Required: false}},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	if hasChangeType(changes, "required_parameter_added") {
		t.Error("optional parameter added should not be a breaking change")
	}
}

func TestBreaking_ParamBecameRequired_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Parameters: []catalog.Parameter{{Name: "sort", In: "query", Required: false}},
	}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Parameters: []catalog.Parameter{{Name: "sort", In: "query", Required: true}},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "parameter_became_required", "HIGH")
}

func TestBreaking_ParamTypeChanged_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/items",
		Parameters: []catalog.Parameter{{Name: "page", In: "query", Schema: schemaType("string")}},
	}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/items",
		Parameters: []catalog.Parameter{{Name: "page", In: "query", Schema: schemaType("integer")}},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "parameter_type_changed", "HIGH")
}

func TestBreaking_ParamTypeChanged_IntToString_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/items",
		Parameters: []catalog.Parameter{{Name: "id", In: "query", Schema: schemaType("integer")}},
	}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/items",
		Parameters: []catalog.Parameter{{Name: "id", In: "query", Schema: schemaType("string")}},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "parameter_type_changed", "HIGH")
}

func TestBreaking_QueryParamRemoved_LOW(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Parameters: []catalog.Parameter{{Name: "sort", In: "query", Required: false}},
	}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/users"}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "parameter_removed", "LOW")
}

func TestBreaking_HeaderParamRemoved_MEDIUM(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Parameters: []catalog.Parameter{{Name: "X-Trace-ID", In: "header"}},
	}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/users"}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "parameter_removed", "MEDIUM")
}

func TestBreaking_RequiredHeaderAdded_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "POST", Path: "/orders"}}
	new := []catalog.Endpoint{{Method: "POST", Path: "/orders",
		Parameters: []catalog.Parameter{{Name: "X-Idempotency-Key", In: "header", Required: true}},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "required_parameter_added", "HIGH")
}

// ── Request: body ─────────────────────────────────────────────────────────────

func TestBreaking_RequiredRequestFieldAdded_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{
			Properties: map[string]catalog.Schema{"name": schemaType("string")},
		},
	}}
	new := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{
			Properties:     map[string]catalog.Schema{"name": schemaType("string"), "email": schemaType("string")},
			RequiredFields: []string{"email"},
		},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "required_request_field_added", "HIGH")
}

func TestBreaking_OptionalRequestFieldAdded_NotBreaking(t *testing.T) {
	old := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{Properties: map[string]catalog.Schema{"name": schemaType("string")}},
	}}
	new := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{
			Properties: map[string]catalog.Schema{"name": schemaType("string"), "bio": schemaType("string")},
		},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	for _, c := range changes {
		if c.Field == "bio" {
			t.Errorf("optional field 'bio' added should not produce breaking change, got %+v", c)
		}
	}
}

func TestBreaking_RequestFieldRemoved_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{
			Properties: map[string]catalog.Schema{"name": schemaType("string"), "age": schemaType("integer")},
		},
	}}
	new := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{
			Properties: map[string]catalog.Schema{"name": schemaType("string")},
		},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "request_field_removed", "HIGH")
}

func TestBreaking_RequestFieldTypeChanged_StringToInt_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{Properties: map[string]catalog.Schema{"age": schemaType("string")}},
	}}
	new := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{Properties: map[string]catalog.Schema{"age": schemaType("integer")}},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "request_field_type_changed", "HIGH")
}

func TestBreaking_RequestFieldTypeChanged_IntToString_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{Properties: map[string]catalog.Schema{"count": schemaType("integer")}},
	}}
	new := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{Properties: map[string]catalog.Schema{"count": schemaType("string")}},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "request_field_type_changed", "HIGH")
}

func TestBreaking_FieldNullableToNonNullable_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{
			Properties: map[string]catalog.Schema{"bio": {Type: "string", Nullable: true}},
		},
	}}
	new := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{
			Properties: map[string]catalog.Schema{"bio": {Type: "string", Nullable: false}},
		},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "field_became_non_nullable", "HIGH")
}

func TestBreaking_FieldNonNullableToNullable_LOW(t *testing.T) {
	old := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{
			Properties: map[string]catalog.Schema{"bio": {Type: "string", Nullable: false}},
		},
	}}
	new := []catalog.Endpoint{{Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{
			Properties: map[string]catalog.Schema{"bio": {Type: "string", Nullable: true}},
		},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "field_became_nullable", "LOW")
}

// ── Response ──────────────────────────────────────────────────────────────────

func TestBreaking_ResponseFieldRemoved_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Responses: map[string]catalog.Response{
			"200": {Properties: map[string]catalog.Schema{"id": schemaType("string"), "name": schemaType("string")}},
		},
	}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Responses: map[string]catalog.Response{
			"200": {Properties: map[string]catalog.Schema{"id": schemaType("string")}},
		},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "response_field_removed", "HIGH")
}

func TestBreaking_ResponseFieldAdded_NotBreaking(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Responses: map[string]catalog.Response{
			"200": {Properties: map[string]catalog.Schema{"id": schemaType("string")}},
		},
	}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Responses: map[string]catalog.Response{
			"200": {Properties: map[string]catalog.Schema{"id": schemaType("string"), "name": schemaType("string")}},
		},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	for _, c := range changes {
		if c.Field == "name" {
			t.Errorf("adding response field 'name' should not break, got: %+v", c)
		}
	}
}

func TestBreaking_ResponseFieldTypeChanged_HIGH(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Responses: map[string]catalog.Response{
			"200": {Properties: map[string]catalog.Schema{"count": schemaType("string")}},
		},
	}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Responses: map[string]catalog.Response{
			"200": {Properties: map[string]catalog.Schema{"count": schemaType("integer")}},
		},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "response_field_type_changed", "HIGH")
}

func TestBreaking_StatusCode200Removed_NoOther2xx_HIGH(t *testing.T) {
	// 200 removed, only 400 remains → HIGH
	old := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Responses: map[string]catalog.Response{"200": {}, "400": {}},
	}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Responses: map[string]catalog.Response{"400": {}},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	c := findChange(changes, "response_status_removed")
	if c == nil {
		t.Fatal("expected response_status_removed")
	}
	if c.Risk != "HIGH" {
		t.Errorf("200 removed with no other 2xx: risk = %q, want HIGH", c.Risk)
	}
}

func TestBreaking_StatusCode200Changed_To201_MEDIUM(t *testing.T) {
	// 200 removed, 201 still present → MEDIUM
	old := []catalog.Endpoint{{Method: "POST", Path: "/users",
		Responses: map[string]catalog.Response{"200": {}, "400": {}},
	}}
	new := []catalog.Endpoint{{Method: "POST", Path: "/users",
		Responses: map[string]catalog.Response{"201": {}, "400": {}},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	c := findChange(changes, "response_status_removed")
	if c == nil {
		t.Fatal("expected response_status_removed")
	}
	if c.Risk != "MEDIUM" {
		t.Errorf("200→201 (other 2xx present): risk = %q, want MEDIUM", c.Risk)
	}
}

func TestBreaking_RequiredResponseFieldBecameOptional_LOW(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Responses: map[string]catalog.Response{
			"200": {
				Properties: map[string]catalog.Schema{"id": schemaType("string")},
				Required:   []string{"id"},
			},
		},
	}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/users",
		Responses: map[string]catalog.Response{
			"200": {Properties: map[string]catalog.Schema{"id": schemaType("string")}},
		},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "response_field_became_optional", "LOW")
}

// ── Meta changes ──────────────────────────────────────────────────────────────

func TestBreaking_DescriptionChanged_LOW(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/users", Description: "old description"}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/users", Description: "new description"}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "description_changed", "LOW")
}

func TestBreaking_DeprecatedAdded_LOW(t *testing.T) {
	old := []catalog.Endpoint{{Method: "GET", Path: "/v1/users", Deprecated: false}}
	new := []catalog.Endpoint{{Method: "GET", Path: "/v1/users", Deprecated: true}}
	changes := catalog.DetectBreakingChanges(old, new)
	assertRisk(t, changes, "deprecated", "LOW")
}

// ── Risk aggregation ──────────────────────────────────────────────────────────

func TestBreaking_OnlyHIGH_OverallHIGH(t *testing.T) {
	changes := []catalog.BreakingChange{{Risk: "HIGH"}}
	if catalog.RiskLevel(changes) != "HIGH" {
		t.Error("only HIGH changes → overall HIGH")
	}
}

func TestBreaking_HIGH_And_MEDIUM_OverallHIGH(t *testing.T) {
	changes := []catalog.BreakingChange{{Risk: "MEDIUM"}, {Risk: "HIGH"}}
	if catalog.RiskLevel(changes) != "HIGH" {
		t.Error("HIGH+MEDIUM → overall HIGH")
	}
}

func TestBreaking_OnlyMEDIUM_OverallMEDIUM(t *testing.T) {
	changes := []catalog.BreakingChange{{Risk: "MEDIUM"}, {Risk: "MEDIUM"}}
	if catalog.RiskLevel(changes) != "MEDIUM" {
		t.Error("only MEDIUM → overall MEDIUM")
	}
}

func TestBreaking_OnlyLOW_OverallLOW(t *testing.T) {
	changes := []catalog.BreakingChange{{Risk: "LOW"}, {Risk: "LOW"}}
	if catalog.RiskLevel(changes) != "LOW" {
		t.Error("only LOW → overall LOW")
	}
}

// ── Multiple changes in one endpoint ─────────────────────────────────────────

func TestBreaking_RequestAndResponseBothChanged(t *testing.T) {
	old := []catalog.Endpoint{{
		Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{
			Properties: map[string]catalog.Schema{"name": schemaType("string")},
		},
		Responses: map[string]catalog.Response{
			"200": {Properties: map[string]catalog.Schema{"id": schemaType("string"), "role": schemaType("string")}},
		},
	}}
	new := []catalog.Endpoint{{
		Method: "POST", Path: "/users",
		RequestBody: &catalog.RequestBody{
			RequiredFields: []string{"email"},
			Properties:     map[string]catalog.Schema{"email": schemaType("string")},
		},
		Responses: map[string]catalog.Response{
			"200": {Properties: map[string]catalog.Schema{"id": schemaType("string")}},
		},
	}}
	changes := catalog.DetectBreakingChanges(old, new)
	hasRequest := hasChangeType(changes, "required_request_field_added") || hasChangeType(changes, "request_field_removed")
	hasResponse := hasChangeType(changes, "response_field_removed")
	if !hasRequest {
		t.Error("expected at least one request-level breaking change")
	}
	if !hasResponse {
		t.Error("expected at least one response-level breaking change")
	}
}

func TestBreaking_100EndpointsChanged_AllReported(t *testing.T) {
	old := make([]catalog.Endpoint, 100)
	new := make([]catalog.Endpoint, 0)
	for i := range old {
		old[i] = ep("GET", fmt.Sprintf("/resource/%d", i))
	}
	changes := catalog.DetectBreakingChanges(old, new)
	if len(changes) < 100 {
		t.Errorf("expected at least 100 breaking changes for 100 removed endpoints, got %d", len(changes))
	}
}

func TestBreaking_FromEquals_To_EmptyResult(t *testing.T) {
	endpoints := []catalog.Endpoint{ep("GET", "/users")}
	changes := catalog.DetectBreakingChanges(endpoints, endpoints)
	if len(changes) != 0 {
		t.Errorf("from==to should produce 0 changes, got %d", len(changes))
	}
}
