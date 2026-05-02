package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	redisAddr := getEnv("REDIS_ADDR", "8.148.255.41:6380")
	redisPassword := getEnv("REDIS_PASSWORD", "Abcd.1234")
	postgresURI := getEnv("POSTGRES_URI", "postgres://tajian:Postgres@1234@pyramidtip.pg.polardb.rds.aliyuncs.com:5432/recommend?sslmode=disable")

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

	log.Println("risk-health-sidecar listening on :8093")
	log.Fatal(http.ListenAndServe(":8093", nil))
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
