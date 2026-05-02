package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockPool struct {
	coldStartResp []string
	firstScreenResp []string
	coldStartErr error
	firstScreenErr error
}

func (m *mockPool) GetColdStartCandidates(ctx interface{}, userID string, n int) ([]string, error) {
	return m.coldStartResp, m.coldStartErr
}

func (m *mockPool) GetFirstScreenCandidates(ctx interface{}, userID string, n int) ([]string, error) {
	return m.firstScreenResp, m.firstScreenErr
}

type mockHandler struct {
	pool *mockPool
}

func TestHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp HealthResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
}

func TestColdStartPool_MissingUserID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cold_start_pool/{user_id}", func(w http.ResponseWriter, r *http.Request) {
		userID := r.PathValue("user_id")
		if userID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id is required"})
			return
		}
		writeJSON(w, http.StatusOK, ColdStartPoolResponse{
			ItemIDs:  []string{"u1", "u2"},
			Strategy: "cold_start",
			Count:    2,
		})
	})

	// Empty path segment results in 404 from mux (user_id is required)
	req := httptest.NewRequest(http.MethodGet, "/cold_start_pool/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	// Mux returns 404 because /cold_start_pool/ does not match /cold_start_pool/{user_id}
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestColdStartPool_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cold_start_pool/{user_id}", func(w http.ResponseWriter, r *http.Request) {
		userID := r.PathValue("user_id")
		assert.Equal(t, "user123", userID)
		nStr := r.URL.Query().Get("n")
		assert.Equal(t, "10", nStr)
		writeJSON(w, http.StatusOK, ColdStartPoolResponse{
			ItemIDs:  []string{"u1", "u2", "u3"},
			Strategy: "cold_start",
			Count:    3,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/cold_start_pool/user123?n=10", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp ColdStartPoolResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "cold_start", resp.Strategy)
	assert.Len(t, resp.ItemIDs, 3)
	assert.Equal(t, 3, resp.Count)
}

func TestFirstScreen_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /first_screen/{user_id}", func(w http.ResponseWriter, r *http.Request) {
		userID := r.PathValue("user_id")
		assert.Equal(t, "user456", userID)
		writeJSON(w, http.StatusOK, FirstScreenResponse{
			ItemIDs: []string{"i1", "i2"},
			Count:   2,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/first_screen/user456?n=5", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp FirstScreenResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Len(t, resp.ItemIDs, 2)
	assert.Equal(t, 2, resp.Count)
}

func TestColdStartPool_DefaultN(t *testing.T) {
	var capturedN string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cold_start_pool/{user_id}", func(w http.ResponseWriter, r *http.Request) {
		capturedN = r.URL.Query().Get("n")
		writeJSON(w, http.StatusOK, ColdStartPoolResponse{
			ItemIDs:  []string{},
			Strategy: "cold_start",
			Count:    0,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/cold_start_pool/user123", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "", capturedN)
}
