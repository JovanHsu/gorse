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

	srv := NewServer(cfg.RedisAddr, cfg.RedisUsername, cfg.RedisPassword, cfg.RedisDB)
	defer srv.Close()

	http.HandleFunc("/health", srv.health)
	http.HandleFunc("GET /ab/experiments", srv.listExperiments)
	http.HandleFunc("GET /ab/assign/{user_id}", srv.assign)
	http.HandleFunc("POST /ab/metrics", srv.recordMetrics)
	http.HandleFunc("GET /ab/report", srv.report)

	port := os.Getenv("HTTP_PORT")
	if port == "" {
		if cfg.HTTPPort > 0 {
			port = ":" + strconv.Itoa(cfg.HTTPPort)
		} else {
			port = ":8092"
		}
	} else {
		port = ":" + port
	}
	log.Printf("ab-experiment-sidecar listening on %s", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal(err)
	}
}
