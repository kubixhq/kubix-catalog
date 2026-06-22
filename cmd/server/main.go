package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/kubixhq/kubix-catalog/internal/catalog"
	"github.com/kubixhq/kubix-catalog/internal/config"
	"github.com/kubixhq/kubix-catalog/internal/db"
	"github.com/kubixhq/kubix-catalog/internal/handler"
)

func main() {
	cfg := config.Load()

	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	database, err := db.Connect(cfg)
	if err != nil {
		log.Fatalf("cannot connect to database: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	store := catalog.NewStore(database)
	h := handler.New(store, cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/catalog/specs", h.IngestSpec)
	mux.HandleFunc("GET /api/catalog/specs/{id}", h.GetSpec)
	mux.HandleFunc("GET /api/catalog/graph", h.Graph)
	mux.HandleFunc("GET /api/catalog/breaking-changes", h.BreakingChanges)
	mux.HandleFunc("GET /api/catalog/services", h.ListServices)
	mux.HandleFunc("GET /api/catalog/services/{id}", h.GetService)
	mux.HandleFunc("DELETE /api/catalog/services/{id}", h.DeleteService)
	mux.HandleFunc("GET /api/catalog/search", h.Search)

	addr := fmt.Sprintf(":%d", cfg.ServerPort)
	log.Printf("kubix-catalog listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
