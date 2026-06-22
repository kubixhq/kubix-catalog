package catalog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// IngestSpec upserts a service record, inserts a new spec version, and refreshes
// the search endpoint table. Returns (serviceID, error). Returns a *ConflictError
// when the (serviceName, specVersion) pair already exists.
func (s *Store) IngestSpec(serviceName, specURL, specVersion string, endpoints []Endpoint, rawJSON []byte) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Upsert service
	var serviceID int
	err = tx.QueryRow(`
		INSERT INTO catalog_services (service_name, spec_url, endpoint_count, last_updated)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (service_name) DO UPDATE
			SET spec_url = EXCLUDED.spec_url,
			    endpoint_count = EXCLUDED.endpoint_count,
			    last_updated = NOW()
		RETURNING id`,
		serviceName, specURL, len(endpoints),
	).Scan(&serviceID)
	if err != nil {
		return 0, fmt.Errorf("upsert service: %w", err)
	}

	// Insert spec (conflict on service_id + spec_version → return ConflictError)
	endpointsJSON, err := json.Marshal(endpoints)
	if err != nil {
		return 0, err
	}
	var specID int
	err = tx.QueryRow(`
		INSERT INTO catalog_specs (service_id, spec_version, endpoints, endpoint_count)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		serviceID, specVersion, endpointsJSON, len(endpoints),
	).Scan(&specID)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, &ConflictError{
				Message: fmt.Sprintf("spec version %q already exists for service %q", specVersion, serviceName),
			}
		}
		return 0, fmt.Errorf("insert spec: %w", err)
	}

	// Refresh search endpoints: delete old entries, insert new
	if _, err := tx.Exec(`DELETE FROM catalog_endpoints WHERE service_id = $1`, serviceID); err != nil {
		return 0, fmt.Errorf("clear endpoints: %w", err)
	}
	for _, ep := range endpoints {
		tags := ep.Tags
		if tags == nil {
			tags = []string{}
		}
		if _, err := tx.Exec(`
			INSERT INTO catalog_endpoints (service_id, spec_id, method, path, description, tags)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			serviceID, specID, ep.Method, ep.Path, ep.Description, pq.Array(tags),
		); err != nil {
			return 0, fmt.Errorf("insert endpoint: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return serviceID, nil
}

// SaveDependencies replaces all outbound dependencies for fromService.
func (s *Store) SaveDependencies(fromService string, toServices []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM catalog_dependencies WHERE from_service = $1`, fromService); err != nil {
		return err
	}
	for _, to := range toServices {
		if _, err := tx.Exec(`
			INSERT INTO catalog_dependencies (from_service, to_service)
			VALUES ($1, $2) ON CONFLICT DO NOTHING`, fromService, to,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetSpec returns the latest spec endpoints for a service.
func (s *Store) GetSpec(serviceID int) (*SpecRecord, error) {
	var rec SpecRecord
	var endpointsJSON []byte
	err := s.db.QueryRow(`
		SELECT cs.id, cs.service_id, sv.service_name, cs.spec_version, cs.endpoints, cs.endpoint_count, cs.ingested_at
		FROM catalog_specs cs
		JOIN catalog_services sv ON sv.id = cs.service_id
		WHERE cs.service_id = $1
		ORDER BY cs.ingested_at DESC
		LIMIT 1`, serviceID,
	).Scan(&rec.ID, &rec.ServiceID, &rec.ServiceName, &rec.SpecVersion, &endpointsJSON, &rec.EndpointCount, &rec.IngestedAt)
	if err == sql.ErrNoRows {
		return nil, &NotFoundError{Message: fmt.Sprintf("service %d not found", serviceID)}
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(endpointsJSON, &rec.Endpoints); err != nil {
		return nil, err
	}
	return &rec, nil
}

// GetSpecVersion returns a specific version's endpoints for breaking-change comparison.
func (s *Store) GetSpecVersion(serviceName, version string) ([]Endpoint, error) {
	var endpointsJSON []byte
	err := s.db.QueryRow(`
		SELECT cs.endpoints
		FROM catalog_specs cs
		JOIN catalog_services sv ON sv.id = cs.service_id
		WHERE sv.service_name = $1 AND cs.spec_version = $2`,
		serviceName, version,
	).Scan(&endpointsJSON)
	if err == sql.ErrNoRows {
		return nil, &NotFoundError{Message: fmt.Sprintf("spec version %q not found for service %q", version, serviceName)}
	}
	if err != nil {
		return nil, err
	}
	var endpoints []Endpoint
	if err := json.Unmarshal(endpointsJSON, &endpoints); err != nil {
		return nil, err
	}
	return endpoints, nil
}

// ListServices returns services filtered by status/search and sorted.
func (s *Store) ListServices(status, search, sort string) ([]ServiceInfo, error) {
	var conditions []string
	var args []interface{}
	i := 1

	if status != "" {
		conditions = append(conditions, fmt.Sprintf("health = $%d", i))
		args = append(args, status)
		i++
	}
	if search != "" {
		conditions = append(conditions, fmt.Sprintf("service_name ILIKE $%d", i))
		args = append(args, "%"+search+"%")
		i++
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	orderBy := "service_name"
	switch sort {
	case "updated":
		orderBy = "last_updated DESC"
	case "name":
		orderBy = "service_name ASC"
	}

	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT id, service_name, spec_url, last_updated, endpoint_count, health
		FROM catalog_services %s ORDER BY %s`, where, orderBy), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var services []ServiceInfo
	for rows.Next() {
		var svc ServiceInfo
		if err := rows.Scan(&svc.ID, &svc.ServiceName, &svc.SpecURL, &svc.LastUpdated, &svc.EndpointCount, &svc.Health); err != nil {
			return nil, err
		}
		services = append(services, svc)
	}
	if services == nil {
		services = []ServiceInfo{}
	}
	return services, rows.Err()
}

// GetService returns a single service by ID.
func (s *Store) GetService(id int) (*ServiceInfo, error) {
	var svc ServiceInfo
	err := s.db.QueryRow(`
		SELECT id, service_name, spec_url, last_updated, endpoint_count, health
		FROM catalog_services WHERE id = $1`, id,
	).Scan(&svc.ID, &svc.ServiceName, &svc.SpecURL, &svc.LastUpdated, &svc.EndpointCount, &svc.Health)
	if err == sql.ErrNoRows {
		return nil, &NotFoundError{Message: fmt.Sprintf("service %d not found", id)}
	}
	if err != nil {
		return nil, err
	}
	return &svc, nil
}

// DeleteService removes a service and all its cascade data.
func (s *Store) DeleteService(id int) error {
	res, err := s.db.Exec(`DELETE FROM catalog_services WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &NotFoundError{Message: fmt.Sprintf("service %d not found", id)}
	}
	return nil
}

// SearchEndpoints searches endpoints across all services.
func (s *Store) SearchEndpoints(q string) ([]SearchResult, error) {
	pattern := "%" + q + "%"
	rows, err := s.db.Query(`
		SELECT e.path, e.method, e.description, e.tags, sv.service_name
		FROM catalog_endpoints e
		JOIN catalog_services sv ON sv.id = e.service_id
		WHERE e.path ILIKE $1 OR e.description ILIKE $1 OR array_to_string(e.tags, ',') ILIKE $1
		ORDER BY sv.service_name, e.path, e.method`,
		pattern,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var tags pq.StringArray
		if err := rows.Scan(&r.Path, &r.Method, &r.Description, &tags, &r.ServiceName); err != nil {
			return nil, err
		}
		r.Tags = []string(tags)
		results = append(results, r)
	}
	if results == nil {
		results = []SearchResult{}
	}
	return results, rows.Err()
}

// AllServicesForGraph returns all services with their latest endpoint count for graph nodes.
func (s *Store) AllServicesForGraph() ([]ServiceInfo, error) {
	return s.ListServices("", "", "name")
}

// AllDependencies returns all declared dependencies.
func (s *Store) AllDependencies() ([]Dependency, error) {
	rows, err := s.db.Query(`SELECT from_service, to_service FROM catalog_dependencies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deps []Dependency
	for rows.Next() {
		var d Dependency
		if err := rows.Scan(&d.FromService, &d.ToService); err != nil {
			return nil, err
		}
		deps = append(deps, d)
	}
	return deps, rows.Err()
}

// AffectedServices returns services that call the given service.
func (s *Store) AffectedServices(serviceName string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT from_service FROM catalog_dependencies WHERE to_service = $1`, serviceName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}
	return result, rows.Err()
}

// GetServiceNameByID returns the service name for an ID.
func (s *Store) GetServiceNameByID(id int) (string, error) {
	var name string
	err := s.db.QueryRow(`SELECT service_name FROM catalog_services WHERE id = $1`, id).Scan(&name)
	if err == sql.ErrNoRows {
		return "", &NotFoundError{Message: fmt.Sprintf("service %d not found", id)}
	}
	return name, err
}

// SpecVersionExists checks whether a given (serviceName, specVersion) already exists.
func (s *Store) SpecVersionExists(serviceName, specVersion string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM catalog_specs cs
			JOIN catalog_services sv ON sv.id = cs.service_id
			WHERE sv.service_name = $1 AND cs.spec_version = $2
		)`, serviceName, specVersion,
	).Scan(&exists)
	return exists, err
}

// LastSpecUpdated returns the ingested_at of the most recent spec for a service.
func (s *Store) LastSpecAt(serviceID int) (time.Time, error) {
	var t time.Time
	err := s.db.QueryRow(`
		SELECT ingested_at FROM catalog_specs WHERE service_id = $1
		ORDER BY ingested_at DESC LIMIT 1`, serviceID,
	).Scan(&t)
	return t, err
}

// error types

type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }

type NotFoundError struct{ Message string }

func (e *NotFoundError) Error() string { return e.Message }

func isUniqueViolation(err error) bool {
	if pqErr, ok := err.(*pq.Error); ok {
		return pqErr.Code == "23505"
	}
	return false
}
