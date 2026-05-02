package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	redisPassword := os.Getenv("REDIS_PASSWORD")
	postgresURI := os.Getenv("POSTGRES_URI")
	if redisAddr == "" || redisPassword == "" || postgresURI == "" {
		log.Fatal("REDIS_ADDR, REDIS_PASSWORD, and POSTGRES_URI must be set")
	}

	store, err := NewStore(redisAddr, redisPassword, postgresURI)
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
		port = ":8093"
	}
	log.Printf("risk-health-sidecar listening on %s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}
