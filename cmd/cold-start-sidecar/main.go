package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorse-io/gorse/config"
	"github.com/gorse-io/gorse/storage"
	"github.com/gorse-io/gorse/storage/data"
)

func main() {
	cfg, err := config.LoadSidecarConfig()
	if err != nil {
		log.Fatal(err)
	}

	store, err := NewStore(cfg.RedisAddr, cfg.RedisUsername, cfg.RedisPassword, cfg.DataStore, "", cfg.RedisDB)
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
	db, err := data.Open(cfg.DataStore, "", dataOpts...)
	if err != nil {
		log.Fatalf("failed to open data store: %v", err)
	}
	defer db.Close()

	pool := NewPool(store, db, "")
	handler := NewHandler(pool)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handler.Health)
	mux.HandleFunc("GET /cold_start_pool/{user_id}", handler.ColdStartPool)
	mux.HandleFunc("GET /first_screen/{user_id}", handler.FirstScreen)

	port := os.Getenv("HTTP_PORT")
	if port == "" {
		if cfg.HTTPPort > 0 {
			port = ":" + strconv.Itoa(cfg.HTTPPort)
		} else {
			port = ":8091"
		}
	} else {
		port = ":" + port
	}
	log.Printf("cold-start-sidecar listening on %s", port)
	if err := http.ListenAndServe(port, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
