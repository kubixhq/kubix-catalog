package catalog

import "time"

type Schema struct {
	Type       string            `json:"type,omitempty"`
	Format     string            `json:"format,omitempty"`
	Nullable   bool              `json:"nullable,omitempty"`
	Properties map[string]Schema `json:"properties,omitempty"`
	Required   []string          `json:"required,omitempty"`
	Items      *Schema           `json:"items,omitempty"`
}

type Parameter struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
	Schema   Schema `json:"schema,omitempty"`
}

type RequestBody struct {
	BodyRequired   bool              `json:"body_required"`
	Properties     map[string]Schema `json:"properties,omitempty"`
	RequiredFields []string          `json:"required_fields,omitempty"`
}

type Response struct {
	Properties map[string]Schema `json:"properties,omitempty"`
	Required   []string          `json:"required,omitempty"`
}

type Endpoint struct {
	Method      string              `json:"method"`
	Path        string              `json:"path"`
	Description string              `json:"description,omitempty"`
	Tags        []string            `json:"tags,omitempty"`
	Deprecated  bool                `json:"deprecated,omitempty"`
	Parameters  []Parameter         `json:"parameters,omitempty"`
	RequestBody *RequestBody        `json:"request_body,omitempty"`
	Responses   map[string]Response `json:"responses,omitempty"`
}

type ParsedSpec struct {
	APIVersion string
	Title      string
	Endpoints  []Endpoint
	Calls      []string
	RawJSON    []byte
}

// DB-backed types

type ServiceInfo struct {
	ID            int       `json:"id"`
	ServiceName   string    `json:"name"`
	SpecURL       string    `json:"specUrl,omitempty"`
	LastUpdated   time.Time `json:"lastUpdated"`
	EndpointCount int       `json:"endpointCount"`
	Health        string    `json:"status"`
}

type SpecRecord struct {
	ID            int       `json:"id"`
	ServiceID     int       `json:"service_id"`
	ServiceName   string    `json:"service_name"`
	SpecVersion   string    `json:"spec_version"`
	EndpointCount int       `json:"endpoint_count"`
	Endpoints     []Endpoint `json:"endpoints"`
	IngestedAt    time.Time `json:"ingested_at"`
}

// Response types

type IngestResponse struct {
	ServiceID     int    `json:"service_id"`
	EndpointCount int    `json:"endpoint_count"`
	Status        string `json:"status"`
}

type GraphNode struct {
	ServiceName   string `json:"service_name"`
	EndpointCount int    `json:"endpoint_count"`
}

type GraphEdge struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	Endpoints []string `json:"endpoints"`
}

type GraphResponse struct {
	Nodes                []GraphNode `json:"nodes"`
	Edges                []GraphEdge `json:"edges"`
	CircularDependencies [][]string  `json:"circular_dependencies"`
}

type BreakingChange struct {
	Type        string `json:"type"`
	Endpoint    string `json:"endpoint,omitempty"`
	Field       string `json:"field,omitempty"`
	Description string `json:"description"`
	Risk        string `json:"risk"`
}

type BreakingChangesResponse struct {
	BreakingChanges  []BreakingChange `json:"breaking_changes"`
	RiskLevel        string           `json:"risk_level"`
	AffectedServices []string         `json:"affected_services"`
}

type SearchResult struct {
	Path        string   `json:"path"`
	Method      string   `json:"method"`
	ServiceName string   `json:"service_name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type Dependency struct {
	FromService string
	ToService   string
}
