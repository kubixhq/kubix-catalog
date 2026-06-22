package handler

import (
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
	if from == to {
		writeJSON(w, http.StatusOK, catalog.BreakingChangesResponse{
			BreakingChanges:  []catalog.BreakingChange{},
			RiskLevel:        "none",
			AffectedServices: []string{},
		})
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

	changes := catalog.DetectBreakingChanges(oldEndpoints, newEndpoints)
	if changes == nil {
		changes = []catalog.BreakingChange{}
	}

	affected, _ := h.store.AffectedServices(service)
	if affected == nil {
		affected = []string{}
	}

	writeJSON(w, http.StatusOK, catalog.BreakingChangesResponse{
		BreakingChanges:  changes,
		RiskLevel:        catalog.RiskLevel(changes),
		AffectedServices: affected,
	})
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
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"services": services,
		"total":    len(services),
	})
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
	writeJSON(w, http.StatusOK, svc)
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
