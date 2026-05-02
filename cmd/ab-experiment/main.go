package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "8.148.255.41:6380"
	}
	password := os.Getenv("REDIS_PASSWORD")
	if password == "" {
		password = "Abcd.1234"
	}

	srv := NewServer(addr, password)
	defer srv.Close()

	http.HandleFunc("/health", srv.health)
	http.HandleFunc("GET /ab/assign/{user_id}", srv.assign)
	http.HandleFunc("POST /ab/metrics", srv.recordMetrics)
	http.HandleFunc("GET /ab/report", srv.report)

	log.Println("AB experiment sidecar listening on :8092")
	if err := http.ListenAndServe(":8092", nil); err != nil {
		log.Fatal(err)
	}
}
