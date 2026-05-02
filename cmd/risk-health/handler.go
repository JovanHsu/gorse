package main

import (
	"encoding/json"
	"net/http"
)

type Handler struct {
	store *Store
}

func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
}

func (h *Handler) RiskScore(w http.ResponseWriter, r *http.Request) {
	var req RiskScoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := h.store.GetRiskScore(r.Context(), req.ItemID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) ComplianceCheck(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	itemID := r.URL.Query().Get("item_id")
	if userID == "" || itemID == "" {
		http.Error(w, "user_id and item_id are required", http.StatusBadRequest)
		return
	}
	resp, err := h.store.CheckCompliance(r.Context(), userID, itemID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) PoolCoverage(w http.ResponseWriter, r *http.Request) {
	resp, err := h.store.GetPoolCoverage(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) FatigueRate(w http.ResponseWriter, r *http.Request) {
	resp, err := h.store.GetFatigueRate(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) SDBalance(w http.ResponseWriter, r *http.Request) {
	resp, err := h.store.GetSDBalance(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(resp)
}
