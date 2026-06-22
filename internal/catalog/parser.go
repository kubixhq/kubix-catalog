package catalog

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func Parse(data []byte) (*ParsedSpec, error) {
	raw, err := decodeToMap(data)
	if err != nil {
		return nil, err
	}

	specType, err := detectSpecType(raw)
	if err != nil {
		return nil, err
	}

	var endpoints []Endpoint
	switch specType {
	case "openapi3":
		endpoints = extractEndpoints3(raw)
	case "swagger2":
		endpoints = extractEndpoints2(raw)
	}

	rawJSON, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("cannot normalize spec to JSON: %w", err)
	}

	info := getMap(raw, "info")

	calls := extractStringSlice(raw, "x-kubix-calls")

	return &ParsedSpec{
		APIVersion: getString(info, "version"),
		Title:      getString(info, "title"),
		Endpoints:  endpoints,
		Calls:      calls,
		RawJSON:    rawJSON,
	}, nil
}

func decodeToMap(data []byte) (map[string]interface{}, error) {
	trimmed := strings.TrimSpace(string(data))
	var raw interface{}

	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("invalid YAML: %w", err)
		}
	}

	normalized := normalizeValue(raw)
	m, ok := normalized.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("spec must be a JSON/YAML object")
	}
	return m, nil
}

// normalizeValue converts map[interface{}]interface{} (yaml.v2 style) to
// map[string]interface{} recursively so json.Marshal works on the result.
func normalizeValue(v interface{}) interface{} {
	switch val := v.(type) {
	case map[interface{}]interface{}:
		m := make(map[string]interface{}, len(val))
		for k, vv := range val {
			m[fmt.Sprintf("%v", k)] = normalizeValue(vv)
		}
		return m
	case map[string]interface{}:
		m := make(map[string]interface{}, len(val))
		for k, vv := range val {
			m[k] = normalizeValue(vv)
		}
		return m
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, vv := range val {
			result[i] = normalizeValue(vv)
		}
		return result
	default:
		return v
	}
}

func detectSpecType(raw map[string]interface{}) (string, error) {
	if v, ok := raw["openapi"].(string); ok && strings.HasPrefix(v, "3.") {
		return "openapi3", nil
	}
	if v, ok := raw["swagger"].(string); ok && strings.HasPrefix(v, "2.") {
		return "swagger2", nil
	}
	return "", fmt.Errorf("not a valid OpenAPI 3.x or Swagger 2.x document")
}

func extractEndpoints3(raw map[string]interface{}) []Endpoint {
	paths, ok := raw["paths"].(map[string]interface{})
	if !ok {
		return nil
	}
	var endpoints []Endpoint
	for path, pathItemRaw := range paths {
		pathItem, ok := pathItemRaw.(map[string]interface{})
		if !ok {
			continue
		}
		for method, opRaw := range pathItem {
			method = strings.ToUpper(method)
			if !isHTTPMethod(method) {
				continue
			}
			op, ok := opRaw.(map[string]interface{})
			if !ok {
				continue
			}
			ep := Endpoint{
				Method:      method,
				Path:        path,
				Description: coalesce(getString(op, "description"), getString(op, "summary")),
				Tags:        extractStringSlice(op, "tags"),
				Deprecated:  getBool(op, "deprecated"),
				Parameters:  extractParameters3(op),
				RequestBody: extractRequestBody3(op),
				Responses:   extractResponses3(op),
			}
			endpoints = append(endpoints, ep)
		}
	}
	return endpoints
}

func extractEndpoints2(raw map[string]interface{}) []Endpoint {
	paths, ok := raw["paths"].(map[string]interface{})
	if !ok {
		return nil
	}
	var endpoints []Endpoint
	for path, pathItemRaw := range paths {
		pathItem, ok := pathItemRaw.(map[string]interface{})
		if !ok {
			continue
		}
		for method, opRaw := range pathItem {
			method = strings.ToUpper(method)
			if !isHTTPMethod(method) {
				continue
			}
			op, ok := opRaw.(map[string]interface{})
			if !ok {
				continue
			}
			ep := Endpoint{
				Method:      method,
				Path:        path,
				Description: coalesce(getString(op, "description"), getString(op, "summary")),
				Tags:        extractStringSlice(op, "tags"),
				Deprecated:  getBool(op, "deprecated"),
				Parameters:  extractParameters2(op),
				RequestBody: extractRequestBody2(op),
				Responses:   extractResponses2(op),
			}
			endpoints = append(endpoints, ep)
		}
	}
	return endpoints
}

func extractParameters3(op map[string]interface{}) []Parameter {
	paramsRaw, ok := op["parameters"].([]interface{})
	if !ok {
		return nil
	}
	var params []Parameter
	for _, pRaw := range paramsRaw {
		p, ok := pRaw.(map[string]interface{})
		if !ok {
			continue
		}
		schema := extractSchema(getMap(p, "schema"))
		params = append(params, Parameter{
			Name:     getString(p, "name"),
			In:       getString(p, "in"),
			Required: getBool(p, "required"),
			Schema:   schema,
		})
	}
	return params
}

func extractParameters2(op map[string]interface{}) []Parameter {
	paramsRaw, ok := op["parameters"].([]interface{})
	if !ok {
		return nil
	}
	var params []Parameter
	for _, pRaw := range paramsRaw {
		p, ok := pRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if getString(p, "in") == "body" {
			continue // body params handled by extractRequestBody2
		}
		params = append(params, Parameter{
			Name:     getString(p, "name"),
			In:       getString(p, "in"),
			Required: getBool(p, "required"),
			Schema: Schema{
				Type: coalesce(getString(p, "type"), getString(getMap(p, "schema"), "type")),
			},
		})
	}
	return params
}

func extractRequestBody3(op map[string]interface{}) *RequestBody {
	rb, ok := op["requestBody"].(map[string]interface{})
	if !ok {
		return nil
	}
	content := getMap(rb, "content")
	schema := findJSONSchema3(content)
	if schema == nil {
		return &RequestBody{BodyRequired: getBool(rb, "required")}
	}
	return &RequestBody{
		BodyRequired:   getBool(rb, "required"),
		Properties:     extractSchemaProperties(schema),
		RequiredFields: extractStringSlice(schema, "required"),
	}
}

func extractRequestBody2(op map[string]interface{}) *RequestBody {
	paramsRaw, ok := op["parameters"].([]interface{})
	if !ok {
		return nil
	}
	for _, pRaw := range paramsRaw {
		p, ok := pRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if getString(p, "in") != "body" {
			continue
		}
		schema := getMap(p, "schema")
		return &RequestBody{
			BodyRequired:   getBool(p, "required"),
			Properties:     extractSchemaProperties(schema),
			RequiredFields: extractStringSlice(schema, "required"),
		}
	}
	return nil
}

func extractResponses3(op map[string]interface{}) map[string]Response {
	responsesRaw, ok := op["responses"].(map[string]interface{})
	if !ok {
		return nil
	}
	result := make(map[string]Response)
	for code, rRaw := range responsesRaw {
		r, ok := rRaw.(map[string]interface{})
		if !ok {
			continue
		}
		content := getMap(r, "content")
		schema := findJSONSchema3(content)
		if schema == nil {
			result[code] = Response{}
			continue
		}
		result[code] = Response{
			Properties: extractSchemaProperties(schema),
			Required:   extractStringSlice(schema, "required"),
		}
	}
	return result
}

func extractResponses2(op map[string]interface{}) map[string]Response {
	responsesRaw, ok := op["responses"].(map[string]interface{})
	if !ok {
		return nil
	}
	result := make(map[string]Response)
	for code, rRaw := range responsesRaw {
		r, ok := rRaw.(map[string]interface{})
		if !ok {
			continue
		}
		schema := getMap(r, "schema")
		result[fmt.Sprintf("%v", code)] = Response{
			Properties: extractSchemaProperties(schema),
			Required:   extractStringSlice(schema, "required"),
		}
	}
	return result
}

func findJSONSchema3(content map[string]interface{}) map[string]interface{} {
	for _, mediaType := range []string{"application/json", "application/json; charset=utf-8"} {
		if mt, ok := content[mediaType].(map[string]interface{}); ok {
			if schema := getMap(mt, "schema"); schema != nil {
				return schema
			}
		}
	}
	// Fallback: first media type
	for _, v := range content {
		if mt, ok := v.(map[string]interface{}); ok {
			if schema := getMap(mt, "schema"); schema != nil {
				return schema
			}
		}
	}
	return nil
}

func extractSchema(raw map[string]interface{}) Schema {
	if raw == nil {
		return Schema{}
	}
	s := Schema{
		Type:       getString(raw, "type"),
		Format:     getString(raw, "format"),
		Nullable:   getBool(raw, "nullable"),
		Required:   extractStringSlice(raw, "required"),
		Properties: extractSchemaProperties(raw),
	}
	if itemsRaw := getMap(raw, "items"); itemsRaw != nil {
		items := extractSchema(itemsRaw)
		s.Items = &items
	}
	return s
}

func extractSchemaProperties(raw map[string]interface{}) map[string]Schema {
	propsRaw, ok := raw["properties"].(map[string]interface{})
	if !ok {
		return nil
	}
	props := make(map[string]Schema, len(propsRaw))
	for name, pRaw := range propsRaw {
		if pMap, ok := pRaw.(map[string]interface{}); ok {
			props[name] = extractSchema(pMap)
		}
	}
	return props
}

// helpers

func isHTTPMethod(m string) bool {
	switch m {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE":
		return true
	}
	return false
}

func getMap(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key].(map[string]interface{}); ok {
		return v
	}
	return nil
}

func getString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func getBool(m map[string]interface{}, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	if !ok {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func extractStringSlice(m map[string]interface{}, key string) []string {
	raw, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
