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
	redisAddr := getEnv("REDIS_ADDR", "8.148.255.41:6380")
	redisPassword := getEnv("REDIS_PASSWORD", "Abcd.1234")
	dataStoreURI := getEnv("DATA_STORE_URI", "postgres://tajian:Postgres@1234@pyramidtip.pg.polardb.rds.aliyuncs.com:5432/recommend?sslmode=disable")
	tablePrefix := getEnv("TABLE_PREFIX", "")

	store, err := NewStore(redisAddr, redisPassword, dataStoreURI, tablePrefix)
	if err != nil {
		log.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Verify Redis connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		log.Fatalf("failed to ping Redis: %v", err)
	}

	// Open data store (PostgreSQL)
	dataOpts := storageOptions(dataStoreURI)
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

	addr := getEnv("HTTP_PORT", ":8091")
	log.Printf("cold-start-sidecar listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func storageOptions(path string) []storage.Option {
	opts := []storage.Option{
		storage.WithMaxOpenConns(25),
		storage.WithMaxIdleConns(5),
		storage.WithConnMaxLifetime(time.Hour),
	}
	return opts
}
