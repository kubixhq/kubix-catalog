<div align="center">

<img src="https://img.shields.io/badge/license-Apache%202.0-blue.svg" alt="License" />
<img src="https://img.shields.io/badge/status-early%20development-orange.svg" alt="Status" />
<img src="https://img.shields.io/badge/go-1.24+-00ADD8.svg" alt="Go Version" />
<img src="https://img.shields.io/badge/PRs-welcome-brightgreen.svg" alt="PRs Welcome" />

# kubix-catalog

**API catalog and dependency graph for your microservices.**

Register OpenAPI/Swagger specs, visualize service dependencies, detect breaking changes, and search across all endpoints — served as a REST API.

[Getting Started](#getting-started) · [Endpoints](#endpoints) · [Declaring Dependencies](#declaring-dependencies) · [Configuration](#configuration) · [Contributing](#contributing)

</div>

---

## What it does

`kubix-catalog` ingests OpenAPI 3.x and Swagger 2.x specs and exposes eight endpoints:

| Endpoint | What it does |
|----------|-------------|
| `POST /api/catalog/specs` | Register a service spec (URL or inline) |
| `GET /api/catalog/specs/:id` | Fetch the latest spec for a service |
| `GET /api/catalog/graph` | Dependency graph with circular dependency detection |
| `GET /api/catalog/breaking-changes` | Compare two spec versions for breaking changes |
| `GET /api/catalog/services` | List all registered services |
| `GET /api/catalog/services/:id` | Get a single service |
| `DELETE /api/catalog/services/:id` | Remove a service |
| `GET /api/catalog/search` | Search endpoints by path, description, or tag |

Part of the [Kubix](https://github.com/kubixhq/kubix) observability platform.

---

## Getting started

**Requirements:** Go 1.24+ or Docker, and a PostgreSQL database.

### With Docker

```bash
docker run \
  -e DB_HOST=localhost \
  -e DB_PORT=5432 \
  -e DB_NAME=kubix_catalog \
  -e DB_USER=your_user \
  -e DB_PASSWORD=your_password \
  -p 8082:8082 \
  ghcr.io/kubixhq/kubix-catalog:latest
```

### With Docker Compose

```yaml
services:
  kubix-catalog:
    image: ghcr.io/kubixhq/kubix-catalog:latest
    ports:
      - "8082:8082"
    environment:
      DB_HOST: your_db_host
      DB_PORT: 5432
      DB_NAME: kubix_catalog
      DB_USER: your_user
      DB_PASSWORD: your_password
```

### From source

```bash
git clone https://github.com/kubixhq/kubix-catalog.git
cd kubix-catalog
cp .env.example .env
go run ./cmd/server
```

The database schema is created automatically on startup — no migration tool required.

---

## Endpoints

### `POST /api/catalog/specs`

Register a service by providing its spec URL or an inline spec object.

**From a URL:**

```bash
curl -X POST http://localhost:8082/api/catalog/specs \
  -H "Content-Type: application/json" \
  -d '{
    "service_name": "user-service",
    "spec_url": "https://user-service.internal/openapi.json",
    "spec_version": "v2"
  }'
```

**Inline spec:**

```bash
curl -X POST http://localhost:8082/api/catalog/specs \
  -H "Content-Type: application/json" \
  -d '{
    "service_name": "user-service",
    "spec_version": "v2",
    "spec": { "openapi": "3.0.3", "info": { ... }, "paths": { ... } }
  }'
```

`spec_version` is optional — falls back to `info.version` from the spec, then `v1`.

```json
{
  "service_id": 4,
  "endpoint_count": 12,
  "status": "success"
}
```

Accepts OpenAPI 3.x and Swagger 2.x, in JSON or YAML format.

---

### `GET /api/catalog/specs/:service_id`

Returns the latest registered spec for a service, including all parsed endpoints.

```bash
curl http://localhost:8082/api/catalog/specs/4
```

```json
{
  "id": 9,
  "service_id": 4,
  "service_name": "user-service",
  "spec_version": "v2",
  "endpoint_count": 12,
  "endpoints": [
    {
      "method": "GET",
      "path": "/users",
      "description": "List users",
      "tags": ["users"],
      "parameters": [
        { "name": "limit", "in": "query", "required": false, "schema": { "type": "integer" } }
      ]
    }
  ],
  "ingested_at": "2025-06-22T10:00:00Z"
}
```

---

### `GET /api/catalog/graph`

Returns a dependency graph of all registered services. Circular dependencies are detected automatically.

```bash
curl http://localhost:8082/api/catalog/graph
```

```json
{
  "nodes": [
    { "service_name": "user-service",    "endpoint_count": 12 },
    { "service_name": "payment-service", "endpoint_count": 8  },
    { "service_name": "order-service",   "endpoint_count": 15 }
  ],
  "edges": [
    { "from": "order-service", "to": "user-service",    "endpoints": [] },
    { "from": "order-service", "to": "payment-service", "endpoints": [] }
  ],
  "circular_dependencies": []
}
```

Dependencies are declared in the spec itself — see [Declaring Dependencies](#declaring-dependencies).

---

### `GET /api/catalog/breaking-changes`

Compares two registered spec versions of a service and classifies each change by risk level.

```bash
curl "http://localhost:8082/api/catalog/breaking-changes?service=user-service&from=v1&to=v2"
```

```json
{
  "breaking_changes": [
    {
      "type": "endpoint_removed",
      "endpoint": "DELETE /users/{id}",
      "description": "endpoint DELETE /users/{id} has been removed",
      "risk": "HIGH"
    },
    {
      "type": "required_request_field_added",
      "endpoint": "POST /users",
      "field": "email",
      "description": "required request body field \"email\" added",
      "risk": "HIGH"
    },
    {
      "type": "response_field_became_optional",
      "endpoint": "GET /users",
      "field": "role",
      "description": "response field \"role\" in status 200 changed from required to optional",
      "risk": "LOW"
    }
  ],
  "risk_level": "HIGH",
  "affected_services": ["order-service", "admin-service"]
}
```

**Risk classification:**

| Risk | Triggers |
|------|---------|
| `HIGH` | Endpoint removed, method changed, required field added, field removed, type changed, nullable→non-nullable |
| `MEDIUM` | Header removed, success status code removed (another 2xx still present) |
| `LOW` | Required→optional in response, non-nullable→nullable, description changed, deprecated flag added |
| `none` | No differences detected |

`affected_services` lists the services that declared a dependency on this service — they are the ones at risk.

---

### `GET /api/catalog/services`

Lists all registered services. Supports filtering and sorting.

```bash
# All services
curl http://localhost:8082/api/catalog/services

# Filter by status
curl "http://localhost:8082/api/catalog/services?status=active"

# Search by name (case-insensitive)
curl "http://localhost:8082/api/catalog/services?search=user"

# Sort by name or last updated
curl "http://localhost:8082/api/catalog/services?sort=name"
curl "http://localhost:8082/api/catalog/services?sort=updated"
```

```json
{
  "services": [
    {
      "id": 4,
      "service_name": "user-service",
      "spec_url": "https://user-service.internal/openapi.json",
      "last_updated": "2025-06-22T10:00:00Z",
      "endpoint_count": 12,
      "health": "active"
    }
  ],
  "total": 1
}
```

---

### `GET /api/catalog/services/:id`

Returns a single service by ID.

```bash
curl http://localhost:8082/api/catalog/services/4
```

---

### `DELETE /api/catalog/services/:id`

Removes a service and all its specs and endpoints.

```bash
curl -X DELETE http://localhost:8082/api/catalog/services/4
# → 204 No Content
```

---

### `GET /api/catalog/search`

Searches all endpoints across all services by path, description, or tag. Minimum query length is 2 characters.

```bash
curl "http://localhost:8082/api/catalog/search?q=users"
```

```json
{
  "results": [
    {
      "path": "/users",
      "method": "GET",
      "service_name": "user-service",
      "description": "List users",
      "tags": ["users"]
    },
    {
      "path": "/admin/users",
      "method": "GET",
      "service_name": "admin-service",
      "description": "Admin user list",
      "tags": ["admin", "users"]
    }
  ]
}
```

---

## Declaring dependencies

kubix-catalog builds the dependency graph from a custom extension field in your spec. Add `x-kubix-calls` at the root of your OpenAPI or Swagger document listing the service names it calls:

```yaml
openapi: "3.0.3"
info:
  title: Order Service
  version: v1

x-kubix-calls:
  - user-service
  - payment-service
  - notification-service

paths:
  /orders:
    post:
      ...
```

When this spec is ingested, kubix-catalog records that `order-service` depends on the three listed services and reflects those edges in the dependency graph. Circular dependencies (`A → B → A`) are detected and reported automatically.

---

## Error responses

All errors return a JSON object with an `error` field:

```json
{ "error": "spec version \"v1\" already exists for service \"user-service\"" }
```

| Status | Cause |
|--------|-------|
| `400` | Invalid request body, missing required fields, invalid spec format |
| `404` | Service or spec version not found |
| `409` | Spec version already registered for this service |
| `413` | Spec exceeds `MAX_SPEC_SIZE_MB` |
| `503` | Spec URL unreachable |

---

## Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DB_HOST` | ✅ | — | PostgreSQL host |
| `DB_PORT` | — | `5432` | PostgreSQL port |
| `DB_NAME` | ✅ | — | Database name |
| `DB_USER` | ✅ | — | Database user |
| `DB_PASSWORD` | ✅ | — | Database password |
| `DB_SSL_MODE` | — | `disable` | `disable` / `require` |
| `SERVER_PORT` | — | `8082` | HTTP server port |
| `SPEC_FETCH_TIMEOUT_SEC` | — | `10` | Timeout when fetching a spec from a URL |
| `MAX_SPEC_SIZE_MB` | — | `10` | Maximum allowed spec file size |

---

## Development

```bash
# Run unit tests
go test ./...

# Run with race detector
go test -race ./...

# Run integration tests (requires PostgreSQL)
TEST_DB_DSN="host=localhost port=5432 dbname=kubix_catalog_test user=postgres password=postgres sslmode=disable" \
  go test -tags integration -race ./...

# Build binary
go build -o kubix-catalog ./cmd/server

# Build Docker image
docker build -t kubix-catalog .
```

---

## Contributing

See the org-wide [CONTRIBUTING.md](https://github.com/kubixhq/kubix/blob/main/CONTRIBUTING.md) for guidelines.

Good first issues are tagged [`good first issue`](https://github.com/kubixhq/kubix-catalog/issues?q=is%3Aissue+label%3A%22good+first+issue%22).

---

## License

Apache 2.0 — see [LICENSE](./LICENSE) for details.

---

<div align="center">
Part of <a href="https://github.com/kubixhq/kubix">Kubix</a> — built in public by <a href="https://github.com/kubixhq">kubixhq</a>
</div>
