package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("REDIS_ADDR")
	password := os.Getenv("REDIS_PASSWORD")
	if addr == "" || password == "" {
		log.Fatal("REDIS_ADDR and REDIS_PASSWORD must be set")
	}

	srv := NewServer(addr, password)
	defer srv.Close()

	http.HandleFunc("/health", srv.health)
	http.HandleFunc("GET /ab/assign/{user_id}", srv.assign)
	http.HandleFunc("POST /ab/metrics", srv.recordMetrics)
	http.HandleFunc("GET /ab/report", srv.report)

	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = ":8092"
	}
	log.Printf("AB experiment sidecar listening on %s", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal(err)
	}
}
