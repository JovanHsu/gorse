package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthHandler(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	srv.health(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "ok") {
		t.Errorf("expected body to contain ok, got %s", body)
	}
}

func TestAssignHandler_MissingParams(t *testing.T) {
	srv := &Server{}

	req := httptest.NewRequest("GET", "/ab/assign/user1?experiment=", nil)
	w := httptest.NewRecorder()

	srv.assign(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestRecordMetricsHandler_InvalidBody(t *testing.T) {
	srv := &Server{}

	req := httptest.NewRequest("POST", "/ab/metrics", strings.NewReader("{invalid}"))
	w := httptest.NewRecorder()

	srv.recordMetrics(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestRecordMetricsHandler_MissingFields(t *testing.T) {
	srv := &Server{}

	body := `{"experiment":"","user_id":"u1","metric":"m1","value":0.5}`
	req := httptest.NewRequest("POST", "/ab/metrics", strings.NewReader(body))
	w := httptest.NewRecorder()

	srv.recordMetrics(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestReportHandler_MissingExperiment(t *testing.T) {
	srv := &Server{}

	req := httptest.NewRequest("GET", "/ab/report", nil)
	w := httptest.NewRecorder()

	srv.report(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}
