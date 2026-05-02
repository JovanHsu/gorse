package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type Handler struct {
	pool *Pool
}

func NewHandler(pool *Pool) *Handler {
	return &Handler{pool: pool}
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
}

func (h *Handler) ColdStartPool(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required"})
		return
	}
	n := parseN(r)

	pool, err := h.pool.GetColdStartCandidates(r.Context(), userID, n)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ColdStartPoolResponse{
		ItemIDs:  pool,
		Strategy: "cold_start",
		Count:    len(pool),
	})
}

func (h *Handler) FirstScreen(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("user_id")
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required"})
		return
	}
	n := parseN(r)

	pool, err := h.pool.GetFirstScreenCandidates(r.Context(), userID, n)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, FirstScreenResponse{
		ItemIDs: pool,
		Count:   len(pool),
	})
}

func parseN(r *http.Request) int {
	nStr := r.URL.Query().Get("n")
	n := 20
	if nStr != "" {
		if parsed, err := strconv.Atoi(nStr); err == nil && parsed > 0 {
			n = parsed
		}
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
