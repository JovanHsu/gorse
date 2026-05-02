package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorse-io/gorse/storage/data"
	"github.com/gorse-io/gorse/storage"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	redisPassword := os.Getenv("REDIS_PASSWORD")
	dataStoreURI := os.Getenv("DATA_STORE_URI")
	tablePrefix := os.Getenv("TABLE_PREFIX")

	if redisAddr == "" || redisPassword == "" || dataStoreURI == "" {
		log.Fatal("REDIS_ADDR, REDIS_PASSWORD, and DATA_STORE_URI must be set")
	}

	store, err := NewStore(redisAddr, redisPassword, dataStoreURI, tablePrefix)
	if err != nil {
		log.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		log.Fatalf("failed to ping Redis: %v", err)
	}

	dataOpts := []storage.Option{
		storage.WithMaxOpenConns(25),
		storage.WithMaxIdleConns(5),
		storage.WithConnMaxLifetime(time.Hour),
	}
	db, err := data.Open(dataStoreURI, tablePrefix, dataOpts...)
	if err != nil {
		log.Fatalf("failed to open data store: %v", err)
	}
	defer db.Close()

	pool := NewPool(store, db, tablePrefix)
	handler := NewHandler(pool)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handler.Health)
	mux.HandleFunc("GET /cold_start_pool/{user_id}", handler.ColdStartPool)
	mux.HandleFunc("GET /first_screen/{user_id}", handler.FirstScreen)

	addr := os.Getenv("HTTP_PORT")
	if addr == "" {
		addr = ":8091"
	}
	log.Printf("cold-start-sidecar listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
