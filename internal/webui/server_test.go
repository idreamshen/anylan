package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlersServeIndexAndStatus(t *testing.T) {
	server := Server{
		Title: "test status",
		Snapshot: func() any {
			return map[string]string{"state": "ok"}
		},
	}

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRec := httptest.NewRecorder()
	server.handleIndex(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("index status = %d", indexRec.Code)
	}
	if !strings.Contains(indexRec.Body.String(), "test status") {
		t.Fatal("index did not include title")
	}
	if !strings.Contains(indexRec.Body.String(), `/static/app.js`) {
		t.Fatal("index did not include app script")
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	statusRec := httptest.NewRecorder()
	server.handleStatus(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status code = %d", statusRec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(statusRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status failed: %v", err)
	}
	if body["state"] != "ok" {
		t.Fatalf("state = %q", body["state"])
	}
}

func TestAuthMiddlewareRequiresBearerToken(t *testing.T) {
	server := Server{Token: "secret"}
	handler := server.withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("authenticated status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
