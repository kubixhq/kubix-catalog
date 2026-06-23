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
	h := handler.NewWithDB(store, database, cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /api/catalog/specs", h.IngestSpec)
	mux.HandleFunc("GET /api/catalog/specs/{id}", h.GetSpec)
	mux.HandleFunc("GET /api/catalog/graph", h.Graph)
	mux.HandleFunc("GET /api/catalog/breaking-changes", h.BreakingChanges)
	mux.HandleFunc("POST /api/catalog/services", h.CreateService)
	mux.HandleFunc("GET /api/catalog/services", h.ListServices)
	mux.HandleFunc("GET /api/catalog/services/{id}", h.GetService)
	mux.HandleFunc("GET /api/catalog/services/{id}/versions", h.ListVersions)
	mux.HandleFunc("POST /api/catalog/services/{name}/dependencies", h.AddDependencies)
	mux.HandleFunc("DELETE /api/catalog/services/{id}", h.DeleteService)
	mux.HandleFunc("GET /api/catalog/search", h.Search)

	addr := fmt.Sprintf(":%d", cfg.ServerPort)
	log.Printf("kubix-catalog listening on %s", addr)
	if err := http.ListenAndServe(addr, cors(mux)); err != nil {
		log.Fatal(err)
	}
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
