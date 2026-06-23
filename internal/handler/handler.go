package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kubixhq/kubix-catalog/internal/catalog"
	"github.com/kubixhq/kubix-catalog/internal/config"
)

type Handler struct {
	store  *catalog.Store
	cfg    config.Config
	client *http.Client
	db     *sql.DB
}

func New(store *catalog.Store, cfg config.Config) *Handler {
	return &Handler{
		store: store,
		cfg:   cfg,
		client: &http.Client{
			Timeout: time.Duration(cfg.SpecFetchTimeoutSec) * time.Second,
		},
	}
}

func NewWithDB(store *catalog.Store, db *sql.DB, cfg config.Config) *Handler {
	return &Handler{
		store:  store,
		db:     db,
		cfg:    cfg,
		client: &http.Client{Timeout: time.Duration(cfg.SpecFetchTimeoutSec) * time.Second},
	}
}

// ensure context is used in ListVersions
var _ = context.Background

// POST /api/catalog/specs
func (h *Handler) IngestSpec(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServiceName string          `json:"service_name"`
		SpecURL     string          `json:"spec_url"`
		SpecVersion string          `json:"spec_version"`
		Spec        json.RawMessage `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.ServiceName == "" {
		writeError(w, http.StatusBadRequest, "service_name is required")
		return
	}
	if req.SpecURL == "" && len(req.Spec) == 0 {
		writeError(w, http.StatusBadRequest, "either spec_url or spec must be provided")
		return
	}

	var rawData []byte

	if req.SpecURL != "" {
		data, err := h.fetchSpec(req.SpecURL)
		if err != nil {
			writeError(w, statusForFetchErr(err), err.Error())
			return
		}
		rawData = data
	} else {
		rawData = []byte(req.Spec)
	}

	maxBytes := int64(h.cfg.MaxSpecSizeMB) * 1024 * 1024
	if int64(len(rawData)) > maxBytes {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("spec exceeds maximum allowed size of %dMB", h.cfg.MaxSpecSizeMB))
		return
	}

	parsed, err := catalog.Parse(rawData)
	if err != nil {
		writeError(w, http.StatusBadRequest, "spec parse error: "+err.Error())
		return
	}

	version := req.SpecVersion
	if version == "" {
		version = parsed.APIVersion
	}
	if version == "" {
		version = "v1"
	}

	serviceID, err := h.store.IngestSpec(req.ServiceName, req.SpecURL, version, parsed.Endpoints, parsed.RawJSON)
	if err != nil {
		var ce *catalog.ConflictError
		if asConflict(err, &ce) {
			writeError(w, http.StatusConflict, ce.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save spec")
		return
	}

	if len(parsed.Calls) > 0 {
		_ = h.store.SaveDependencies(req.ServiceName, parsed.Calls)
	}

	writeJSON(w, http.StatusCreated, catalog.IngestResponse{
		ServiceID:     serviceID,
		EndpointCount: len(parsed.Endpoints),
		Status:        "success",
	})
}

// GET /api/catalog/specs/{service_id}
func (h *Handler) GetSpec(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	rec, err := h.store.GetSpec(id)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// GET /api/catalog/graph
func (h *Handler) Graph(w http.ResponseWriter, r *http.Request) {
	services, err := h.store.AllServicesForGraph()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load services")
		return
	}
	deps, err := h.store.AllDependencies()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load dependencies")
		return
	}
	writeJSON(w, http.StatusOK, catalog.BuildGraph(services, deps))
}

// GET /api/catalog/breaking-changes?service=X&from=v1&to=v2
func (h *Handler) BreakingChanges(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	if service == "" || from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "service, from, and to query parameters are required")
		return
	}
	// camelCase response matching the dashboard types
	type apiChange struct {
		RiskLevel  string `json:"riskLevel"`
		ChangeType string `json:"changeType"`
		Details    string `json:"details"`
		Group      string `json:"group"`
	}
	type affectedSvc struct {
		Name          string `json:"name"`
		EndpointCount int    `json:"endpointCount"`
	}
	type apiBreakingResponse struct {
		RiskLevel        string        `json:"riskLevel"`
		Changes          []apiChange   `json:"changes"`
		AffectedServices []affectedSvc `json:"affectedServices"`
	}

	emptyResp := apiBreakingResponse{
		RiskLevel:        "NONE",
		Changes:          []apiChange{},
		AffectedServices: []affectedSvc{},
	}

	if from == to {
		writeJSON(w, http.StatusOK, emptyResp)
		return
	}

	oldEndpoints, err := h.store.GetSpecVersion(service, from)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}
	newEndpoints, err := h.store.GetSpecVersion(service, to)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}

	rawChanges := catalog.DetectBreakingChanges(oldEndpoints, newEndpoints)
	if rawChanges == nil {
		rawChanges = []catalog.BreakingChange{}
	}

	apiChanges := make([]apiChange, 0, len(rawChanges))
	for _, c := range rawChanges {
		group := "Endpoint"
		if strings.Contains(c.Type, "request") || strings.Contains(c.Type, "param") {
			group = "Request"
		} else if strings.Contains(c.Type, "response") || strings.Contains(c.Type, "field") {
			group = "Response"
		}
		riskLvl := strings.ToUpper(c.Risk)
		if riskLvl == "" {
			riskLvl = "MEDIUM"
		}
		apiChanges = append(apiChanges, apiChange{
			RiskLevel:  riskLvl,
			ChangeType: c.Type,
			Details:    c.Description,
			Group:      group,
		})
	}

	affectedNames, _ := h.store.AffectedServices(service)
	if affectedNames == nil {
		affectedNames = []string{}
	}
	affected := make([]affectedSvc, 0, len(affectedNames))
	for _, name := range affectedNames {
		affected = append(affected, affectedSvc{Name: name, EndpointCount: 0})
	}

	overallRisk := strings.ToUpper(catalog.RiskLevel(rawChanges))
	if overallRisk == "" {
		overallRisk = "NONE"
	}

	writeJSON(w, http.StatusOK, apiBreakingResponse{
		RiskLevel:        overallRisk,
		Changes:          apiChanges,
		AffectedServices: affected,
	})
}

// POST /api/catalog/services  { name, specUrl }
func (h *Handler) CreateService(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		SpecURL string `json:"specUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.SpecURL == "" {
		writeError(w, http.StatusBadRequest, "name and specUrl are required")
		return
	}

	rawData, err := h.fetchSpec(req.SpecURL)
	if err != nil {
		writeError(w, statusForFetchErr(err), err.Error())
		return
	}

	parsed, err := catalog.Parse(rawData)
	if err != nil {
		writeError(w, http.StatusBadRequest, "spec parse error: "+err.Error())
		return
	}

	version := parsed.APIVersion
	if version == "" {
		version = "v1"
	}

	serviceID, err := h.store.IngestSpec(req.Name, req.SpecURL, version, parsed.Endpoints, parsed.RawJSON)
	if err != nil {
		var ce *catalog.ConflictError
		if asConflict(err, &ce) {
			writeError(w, http.StatusConflict, ce.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to save service")
		return
	}

	svc, err := h.store.GetService(serviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "service created but could not be retrieved")
		return
	}
	writeJSON(w, http.StatusCreated, svc)
}

// GET /api/catalog/services
func (h *Handler) ListServices(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	search := r.URL.Query().Get("search")
	sort := r.URL.Query().Get("sort")

	services, err := h.store.ListServices(status, search, sort)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list services")
		return
	}
	if services == nil {
		services = []catalog.ServiceInfo{}
	}
	writeJSON(w, http.StatusOK, services)
}

// GET /api/catalog/services/{id}
func (h *Handler) GetService(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	svc, err := h.store.GetService(id)
	if err != nil {
		h.handleStoreError(w, err)
		return
	}

	type apiEndpoint struct {
		Method      string   `json:"method"`
		Path        string   `json:"path"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}
	type serviceDetail struct {
		ID            int           `json:"id"`
		Name          string        `json:"name"`
		SpecURL       string        `json:"specUrl,omitempty"`
		LastUpdated   string        `json:"lastUpdated"`
		EndpointCount int           `json:"endpointCount"`
		Status        string        `json:"status"`
		Endpoints     []apiEndpoint `json:"endpoints"`
	}

	detail := serviceDetail{
		ID:            svc.ID,
		Name:          svc.ServiceName,
		SpecURL:       svc.SpecURL,
		LastUpdated:   svc.LastUpdated.Format("2006-01-02T15:04:05Z07:00"),
		EndpointCount: svc.EndpointCount,
		Status:        svc.Health,
		Endpoints:     []apiEndpoint{},
	}

	if spec, err := h.store.GetSpec(id); err == nil {
		for _, ep := range spec.Endpoints {
			tags := ep.Tags
			if tags == nil {
				tags = []string{}
			}
			detail.Endpoints = append(detail.Endpoints, apiEndpoint{
				Method:      ep.Method,
				Path:        ep.Path,
				Description: ep.Description,
				Tags:        tags,
			})
		}
		detail.EndpointCount = len(detail.Endpoints)
	}

	writeJSON(w, http.StatusOK, detail)
}

// POST /api/catalog/services/{name}/dependencies  {"dependsOn":["svc-a","svc-b"]}
func (h *Handler) AddDependencies(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "service name required")
		return
	}
	var req struct {
		DependsOn []string `json:"dependsOn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := h.store.SaveDependencies(name, req.DependsOn); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save dependencies")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// GET /api/catalog/services/{id}/versions
func (h *Handler) ListVersions(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	type versionInfo struct {
		SpecVersion   string `json:"specVersion"`
		EndpointCount int    `json:"endpointCount"`
		IngestedAt    string `json:"ingestedAt"`
	}
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT cs.spec_version, cs.endpoint_count, cs.ingested_at::text
		FROM catalog_specs cs
		WHERE cs.service_id = $1
		ORDER BY cs.ingested_at DESC
	`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list versions")
		return
	}
	defer rows.Close()
	var versions []versionInfo
	for rows.Next() {
		var v versionInfo
		if err := rows.Scan(&v.SpecVersion, &v.EndpointCount, &v.IngestedAt); err == nil {
			versions = append(versions, v)
		}
	}
	if versions == nil {
		versions = []versionInfo{}
	}
	writeJSON(w, http.StatusOK, versions)
}

// DELETE /api/catalog/services/{id}
func (h *Handler) DeleteService(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteService(id); err != nil {
		h.handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/catalog/search?q=users
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q query parameter is required")
		return
	}
	if len(q) < 2 {
		writeError(w, http.StatusBadRequest, "q must be at least 2 characters")
		return
	}
	results, err := h.store.SearchEndpoints(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": results})
}

// spec fetching

type fetchError struct {
	status  int
	message string
}

func (e *fetchError) Error() string { return e.message }

func (h *Handler) fetchSpec(url string) ([]byte, error) {
	resp, err := h.client.Get(url)
	if err != nil {
		return nil, &fetchError{http.StatusServiceUnavailable, "spec URL unreachable: " + err.Error()}
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		return nil, &fetchError{http.StatusBadRequest, "URL returned HTML, not an API spec"}
	}

	maxBytes := int64(h.cfg.MaxSpecSizeMB) * 1024 * 1024
	limited := io.LimitReader(resp.Body, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, &fetchError{http.StatusServiceUnavailable, "failed to read spec: " + err.Error()}
	}
	if int64(len(data)) > maxBytes {
		return nil, &fetchError{
			http.StatusRequestEntityTooLarge,
			fmt.Sprintf("spec exceeds maximum allowed size of %dMB", h.cfg.MaxSpecSizeMB),
		}
	}
	return data, nil
}

func statusForFetchErr(err error) int {
	if fe, ok := err.(*fetchError); ok {
		return fe.status
	}
	return http.StatusInternalServerError
}

// helpers

func parseID(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.PathValue("id")
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func (h *Handler) handleStoreError(w http.ResponseWriter, err error) {
	var nfe *catalog.NotFoundError
	if asNotFound(err, &nfe) {
		writeError(w, http.StatusNotFound, nfe.Error())
		return
	}
	var ce *catalog.ConflictError
	if asConflict(err, &ce) {
		writeError(w, http.StatusConflict, ce.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "internal error")
}

func asNotFound(err error, target **catalog.NotFoundError) bool {
	if nfe, ok := err.(*catalog.NotFoundError); ok {
		*target = nfe
		return true
	}
	return false
}

func asConflict(err error, target **catalog.ConflictError) bool {
	if ce, ok := err.(*catalog.ConflictError); ok {
		*target = ce
		return true
	}
	return false
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
