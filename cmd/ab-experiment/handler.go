package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/redis/go-redis/v9"
)

type Server struct {
	rdb *redis.Client
}

func NewServer(addr, password string) *Server {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})
	return &Server{rdb: rdb}
}

func (s *Server) Close() error {
	return s.rdb.Close()
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) assign(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	experiment := r.URL.Query().Get("experiment")
	if userID == "" || experiment == "" {
		http.Error(w, "missing user_id or experiment", http.StatusBadRequest)
		return
	}

	group := AssignGroup(userID, experiment)
	ctx := r.Context()
	if err := s.storeAssign(ctx, experiment, userID, group); err != nil {
		log.Printf("store assign error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(AssignResponse{
		UserID:     userID,
		Experiment: experiment,
		Group:      group,
	})
}

func (s *Server) recordMetrics(w http.ResponseWriter, r *http.Request) {
	var rec MetricRecord
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if rec.Experiment == "" || rec.UserID == "" || rec.Metric == "" {
		http.Error(w, "missing required fields", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	group := AssignGroup(rec.UserID, rec.Experiment)
	if err := s.recordMetric(ctx, rec.Experiment, rec.UserID, rec.Metric, rec.Value, group); err != nil {
		log.Printf("record metric error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.recordUser(ctx, rec.Experiment, rec.UserID, group); err != nil {
		log.Printf("record user error: %v", err)
	}

	json.NewEncoder(w).Encode(MetricResponse{Status: "recorded"})
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	experiment := r.URL.Query().Get("experiment")
	if experiment == "" {
		http.Error(w, "missing experiment", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	rep, err := s.generateReport(ctx, experiment)
	if err != nil {
		log.Printf("generate report error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(rep)
}
