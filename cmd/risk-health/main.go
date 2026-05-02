package main

import (
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gorse-io/gorse/config"
)

func main() {
	cfg, err := config.LoadSidecarConfig()
	if err != nil {
		log.Fatal(err)
	}

	store, err := NewStore(cfg.RedisAddr, cfg.RedisUsername, cfg.RedisPassword, cfg.DataStore, cfg.RedisDB)
	if err != nil {
		log.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	h := NewHandler(store)

	http.HandleFunc("/health", h.Health)
	http.HandleFunc("/api/risk/score", h.RiskScore)
	http.HandleFunc("/api/compliance/check", h.ComplianceCheck)
	http.HandleFunc("/api/health/pool_coverage", h.PoolCoverage)
	http.HandleFunc("/api/health/fatigue_rate", h.FatigueRate)
	http.HandleFunc("/api/health/sd_balance", h.SDBalance)

	port := os.Getenv("HTTP_PORT")
	if port == "" {
		if cfg.HTTPPort > 0 {
			port = ":" + strconv.Itoa(cfg.HTTPPort)
		} else {
			port = ":8093"
		}
	} else {
		port = ":" + port
	}
	log.Printf("risk-health-sidecar listening on %s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}
