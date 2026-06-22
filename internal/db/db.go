package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"github.com/kubixhq/kubix-catalog/internal/config"
)

func Connect(cfg config.Config) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser, cfg.DBPassword, cfg.DBSSLMode,
	)
	d, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.PingContext(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func Migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS catalog_services (
			id             SERIAL PRIMARY KEY,
			service_name   VARCHAR(255) UNIQUE NOT NULL,
			spec_url       TEXT NOT NULL DEFAULT '',
			endpoint_count INTEGER NOT NULL DEFAULT 0,
			health         VARCHAR(20) NOT NULL DEFAULT 'active',
			ingested_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_updated   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS catalog_specs (
			id             SERIAL PRIMARY KEY,
			service_id     INTEGER NOT NULL REFERENCES catalog_services(id) ON DELETE CASCADE,
			spec_version   VARCHAR(100) NOT NULL,
			endpoints      JSONB NOT NULL DEFAULT '[]',
			endpoint_count INTEGER NOT NULL DEFAULT 0,
			ingested_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(service_id, spec_version)
		);

		CREATE TABLE IF NOT EXISTS catalog_endpoints (
			id          SERIAL PRIMARY KEY,
			service_id  INTEGER NOT NULL REFERENCES catalog_services(id) ON DELETE CASCADE,
			spec_id     INTEGER NOT NULL REFERENCES catalog_specs(id) ON DELETE CASCADE,
			method      VARCHAR(10) NOT NULL,
			path        TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			tags        TEXT[] NOT NULL DEFAULT '{}'
		);

		CREATE TABLE IF NOT EXISTS catalog_dependencies (
			id           SERIAL PRIMARY KEY,
			from_service VARCHAR(255) NOT NULL,
			to_service   VARCHAR(255) NOT NULL,
			UNIQUE(from_service, to_service)
		);
	`)
	return err
}
