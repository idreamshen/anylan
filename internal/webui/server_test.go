package webui

import (
	"context"
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
	if !strings.Contains(indexRec.Body.String(), `<div id="app"`) {
		t.Fatal("index did not include app mount point")
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

func TestAPIHandlers(t *testing.T) {
	server := Server{
		Snapshot: func() any { return map[string]string{"state": "ok"} },
		Devices: func(context.Context, json.RawMessage) (any, error) {
			return []map[string]string{{"name": "anylan0"}}, nil
		},
		Join: func(_ context.Context, payload json.RawMessage) (any, error) {
			var body map[string]string
			if err := json.Unmarshal(payload, &body); err != nil {
				return nil, err
			}
			return map[string]string{"room": body["room"]}, nil
		},
		Leave: func(context.Context, json.RawMessage) (any, error) {
			return map[string]bool{"left": true}, nil
		},
	}
	handler := server.Handler()

	devReq := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	devRec := httptest.NewRecorder()
	handler.ServeHTTP(devRec, devReq)
	if devRec.Code != http.StatusOK || !strings.Contains(devRec.Body.String(), "anylan0") {
		t.Fatalf("devices response = %d %s", devRec.Code, devRec.Body.String())
	}

	joinReq := httptest.NewRequest(http.MethodPost, "/api/join", strings.NewReader(`{"room":"test"}`))
	joinRec := httptest.NewRecorder()
	handler.ServeHTTP(joinRec, joinReq)
	if joinRec.Code != http.StatusOK || !strings.Contains(joinRec.Body.String(), "test") {
		t.Fatalf("join response = %d %s", joinRec.Code, joinRec.Body.String())
	}

	leaveReq := httptest.NewRequest(http.MethodPost, "/api/leave", strings.NewReader(`{}`))
	leaveRec := httptest.NewRecorder()
	handler.ServeHTTP(leaveRec, leaveReq)
	if leaveRec.Code != http.StatusOK || !strings.Contains(leaveRec.Body.String(), "left") {
		t.Fatalf("leave response = %d %s", leaveRec.Code, leaveRec.Body.String())
	}
}

func TestAPIHandlerReportsAPIErrors(t *testing.T) {
	server := Server{
		Snapshot: func() any { return nil },
		Join: func(context.Context, json.RawMessage) (any, error) {
			return nil, APIError{Status: http.StatusConflict, Message: "already joined"}
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/join", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	if !strings.Contains(rec.Body.String(), "already joined") {
		t.Fatalf("body = %s", rec.Body.String())
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
