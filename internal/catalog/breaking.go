package catalog

import "fmt"

func DetectBreakingChanges(old, new []Endpoint) []BreakingChange {
	var changes []BreakingChange

	oldByKey := endpointIndex(old)
	newByKey := endpointIndex(new)
	oldByPath := pathIndex(old)
	newByPath := pathIndex(new)

	// Removed endpoints → HIGH
	for key := range oldByKey {
		if _, exists := newByKey[key]; !exists {
			changes = append(changes, BreakingChange{
				Type:        "endpoint_removed",
				Endpoint:    key,
				Description: fmt.Sprintf("endpoint %s has been removed", key),
				Risk:        "HIGH",
			})
		}
	}

	// Method removed from path → HIGH
	for path, oldMethods := range oldByPath {
		newMethods, exists := newByPath[path]
		if !exists {
			continue
		}
		for _, method := range oldMethods {
			if !containsStr(newMethods, method) {
				changes = append(changes, BreakingChange{
					Type:        "method_changed",
					Endpoint:    method + " " + path,
					Description: fmt.Sprintf("HTTP method %s removed from path %s", method, path),
					Risk:        "HIGH",
				})
			}
		}
	}

	// Per-endpoint comparison
	for key, newEP := range newByKey {
		oldEP, exists := oldByKey[key]
		if !exists {
			continue
		}
		changes = append(changes, compareParams(oldEP, newEP)...)
		changes = append(changes, compareRequestBody(oldEP, newEP)...)
		changes = append(changes, compareResponses(oldEP, newEP)...)
		changes = append(changes, compareEndpointMeta(oldEP, newEP)...)
	}

	return changes
}

func RiskLevel(changes []BreakingChange) string {
	if len(changes) == 0 {
		return "none"
	}
	level := "LOW"
	for _, c := range changes {
		switch c.Risk {
		case "HIGH":
			return "HIGH"
		case "MEDIUM":
			level = "MEDIUM"
		}
	}
	return level
}

func compareParams(old, new Endpoint) []BreakingChange {
	var changes []BreakingChange
	ep := new.Method + " " + new.Path

	oldParams := paramIndex(old.Parameters)
	newParams := paramIndex(new.Parameters)

	for key, newP := range newParams {
		oldP, exists := oldParams[key]
		if !exists {
			if newP.Required {
				// Required header added → HIGH; required others → HIGH
				changes = append(changes, BreakingChange{
					Type:        "required_parameter_added",
					Endpoint:    ep,
					Field:       newP.Name,
					Description: fmt.Sprintf("required parameter %q (in: %s) added", newP.Name, newP.In),
					Risk:        "HIGH",
				})
			}
			continue
		}
		// Optional → required
		if !oldP.Required && newP.Required {
			changes = append(changes, BreakingChange{
				Type:        "parameter_became_required",
				Endpoint:    ep,
				Field:       newP.Name,
				Description: fmt.Sprintf("parameter %q (in: %s) became required", newP.Name, newP.In),
				Risk:        "HIGH",
			})
		}
		// Type changed
		if oldP.Schema.Type != "" && newP.Schema.Type != "" && oldP.Schema.Type != newP.Schema.Type {
			changes = append(changes, BreakingChange{
				Type:        "parameter_type_changed",
				Endpoint:    ep,
				Field:       newP.Name,
				Description: fmt.Sprintf("parameter %q type changed from %s to %s", newP.Name, oldP.Schema.Type, newP.Schema.Type),
				Risk:        "HIGH",
			})
		}
		// Nullable changed
		changes = append(changes, nullableChanges(ep, newP.Name, oldP.Schema.Nullable, newP.Schema.Nullable)...)
	}

	for key, oldP := range oldParams {
		if _, exists := newParams[key]; !exists {
			// Header removal is MEDIUM; others LOW
			risk := "LOW"
			if oldP.In == "header" {
				risk = "MEDIUM"
			}
			changes = append(changes, BreakingChange{
				Type:        "parameter_removed",
				Endpoint:    ep,
				Field:       oldP.Name,
				Description: fmt.Sprintf("parameter %q (in: %s) removed", oldP.Name, oldP.In),
				Risk:        risk,
			})
		}
	}

	return changes
}

func compareRequestBody(old, new Endpoint) []BreakingChange {
	var changes []BreakingChange
	ep := new.Method + " " + new.Path

	if old.RequestBody == nil && new.RequestBody == nil {
		return nil
	}

	if old.RequestBody == nil && new.RequestBody != nil {
		for _, field := range new.RequestBody.RequiredFields {
			changes = append(changes, BreakingChange{
				Type:        "required_request_field_added",
				Endpoint:    ep,
				Field:       field,
				Description: fmt.Sprintf("required request body field %q added", field),
				Risk:        "HIGH",
			})
		}
		return changes
	}

	if old.RequestBody != nil && new.RequestBody != nil {
		oldRequired := strSet(old.RequestBody.RequiredFields)

		// New required fields
		for _, field := range new.RequestBody.RequiredFields {
			if !oldRequired[field] {
				changes = append(changes, BreakingChange{
					Type:        "required_request_field_added",
					Endpoint:    ep,
					Field:       field,
					Description: fmt.Sprintf("required request body field %q added", field),
					Risk:        "HIGH",
				})
			}
		}

		// Field type changed or nullable changed
		for name, oldSchema := range old.RequestBody.Properties {
			if newSchema, exists := new.RequestBody.Properties[name]; exists {
				if oldSchema.Type != "" && newSchema.Type != "" && oldSchema.Type != newSchema.Type {
					changes = append(changes, BreakingChange{
						Type:        "request_field_type_changed",
						Endpoint:    ep,
						Field:       name,
						Description: fmt.Sprintf("request field %q type changed from %s to %s", name, oldSchema.Type, newSchema.Type),
						Risk:        "HIGH",
					})
				}
				changes = append(changes, nullableChanges(ep, name, oldSchema.Nullable, newSchema.Nullable)...)
			} else {
				// Field removed → HIGH
				changes = append(changes, BreakingChange{
					Type:        "request_field_removed",
					Endpoint:    ep,
					Field:       name,
					Description: fmt.Sprintf("request body field %q removed", name),
					Risk:        "HIGH",
				})
			}
		}
	}

	return changes
}

func compareResponses(old, new Endpoint) []BreakingChange {
	var changes []BreakingChange
	ep := new.Method + " " + new.Path

	for code, oldResp := range old.Responses {
		newResp, exists := new.Responses[code]
		if !exists {
			risk := responseStatusRemovedRisk(code, new.Responses)
			changes = append(changes, BreakingChange{
				Type:        "response_status_removed",
				Endpoint:    ep,
				Description: fmt.Sprintf("response status %s removed", code),
				Risk:        risk,
			})
			continue
		}

		// Field removed or type changed
		for name, oldSchema := range oldResp.Properties {
			if newSchema, fieldExists := newResp.Properties[name]; !fieldExists {
				changes = append(changes, BreakingChange{
					Type:        "response_field_removed",
					Endpoint:    ep,
					Field:       name,
					Description: fmt.Sprintf("response field %q removed from status %s", name, code),
					Risk:        "HIGH",
				})
			} else {
				if oldSchema.Type != "" && newSchema.Type != "" && oldSchema.Type != newSchema.Type {
					changes = append(changes, BreakingChange{
						Type:        "response_field_type_changed",
						Endpoint:    ep,
						Field:       name,
						Description: fmt.Sprintf("response field %q type changed from %s to %s in status %s", name, oldSchema.Type, newSchema.Type, code),
						Risk:        "HIGH",
					})
				}
				changes = append(changes, nullableChanges(ep, name, oldSchema.Nullable, newSchema.Nullable)...)
			}
		}

		// Required → optional in response → LOW
		oldRequired := strSet(oldResp.Required)
		newRequired := strSet(newResp.Required)
		for field := range oldRequired {
			if !newRequired[field] {
				changes = append(changes, BreakingChange{
					Type:        "response_field_became_optional",
					Endpoint:    ep,
					Field:       field,
					Description: fmt.Sprintf("response field %q in status %s changed from required to optional", field, code),
					Risk:        "LOW",
				})
			}
		}
	}

	return changes
}

// compareEndpointMeta reports LOW-risk changes for description and deprecated.
func compareEndpointMeta(old, new Endpoint) []BreakingChange {
	var changes []BreakingChange
	ep := new.Method + " " + new.Path

	if old.Description != new.Description && old.Description != "" && new.Description != "" {
		changes = append(changes, BreakingChange{
			Type:        "description_changed",
			Endpoint:    ep,
			Description: "endpoint description changed",
			Risk:        "LOW",
		})
	}
	if !old.Deprecated && new.Deprecated {
		changes = append(changes, BreakingChange{
			Type:        "deprecated",
			Endpoint:    ep,
			Description: fmt.Sprintf("endpoint %s marked as deprecated", ep),
			Risk:        "LOW",
		})
	}

	return changes
}

func nullableChanges(ep, field string, oldNullable, newNullable bool) []BreakingChange {
	if oldNullable == newNullable {
		return nil
	}
	if oldNullable && !newNullable {
		return []BreakingChange{{
			Type:        "field_became_non_nullable",
			Endpoint:    ep,
			Field:       field,
			Description: fmt.Sprintf("field %q changed from nullable to non-nullable", field),
			Risk:        "HIGH",
		}}
	}
	return []BreakingChange{{
		Type:        "field_became_nullable",
		Endpoint:    ep,
		Field:       field,
		Description: fmt.Sprintf("field %q changed from non-nullable to nullable", field),
		Risk:        "LOW",
	}}
}

// responseStatusRemovedRisk returns HIGH if a 2xx is removed and the new spec
// has no remaining 2xx responses, MEDIUM otherwise.
func responseStatusRemovedRisk(removedCode string, newResponses map[string]Response) string {
	if len(removedCode) == 0 || removedCode[0] != '2' {
		return "MEDIUM"
	}
	for code := range newResponses {
		if len(code) > 0 && code[0] == '2' {
			return "MEDIUM"
		}
	}
	return "HIGH"
}

// index helpers

func endpointIndex(endpoints []Endpoint) map[string]Endpoint {
	m := make(map[string]Endpoint, len(endpoints))
	for _, ep := range endpoints {
		m[ep.Method+" "+ep.Path] = ep
	}
	return m
}

func pathIndex(endpoints []Endpoint) map[string][]string {
	m := make(map[string][]string)
	for _, ep := range endpoints {
		m[ep.Path] = append(m[ep.Path], ep.Method)
	}
	return m
}

func paramIndex(params []Parameter) map[string]Parameter {
	m := make(map[string]Parameter, len(params))
	for _, p := range params {
		m[p.In+":"+p.Name] = p
	}
	return m
}

func strSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
