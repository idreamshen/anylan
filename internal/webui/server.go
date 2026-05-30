package webui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

//go:embed dist/* dist/assets/*
var distFiles embed.FS

type Server struct {
	Addr           string
	Title          string
	Token          string
	Snapshot       func() any
	Devices        APIHandler
	Join           APIHandler
	Leave          APIHandler
	Logs           func() any
	Capture        func() any
	CaptureEnable  APIHandler
	CaptureDisable APIHandler
}

type APIHandler func(context.Context, json.RawMessage) (any, error)

type APIError struct {
	Status  int
	Message string
}

func (e APIError) Error() string {
	return e.Message
}

func (s Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.withAuth(s.handleIndex))
	mux.HandleFunc("/api/status", s.withAuth(s.handleStatus))
	mux.HandleFunc("/api/devices", s.withAuth(s.handleAPI(http.MethodGet, s.Devices)))
	mux.HandleFunc("/api/join", s.withAuth(s.handleAPI(http.MethodPost, s.Join)))
	mux.HandleFunc("/api/leave", s.withAuth(s.handleAPI(http.MethodPost, s.Leave)))
	mux.HandleFunc("/api/logs", s.withAuth(s.handleLogs))
	mux.HandleFunc("/api/capture", s.withAuth(s.handleCapture))
	mux.HandleFunc("/api/capture/enable", s.withAuth(s.handleAPI(http.MethodPost, s.CaptureEnable)))
	mux.HandleFunc("/api/capture/disable", s.withAuth(s.handleAPI(http.MethodPost, s.CaptureDisable)))
	mux.HandleFunc("/assets/", s.withAuth(s.handleAsset))
	return mux
}

func (s Server) ListenAndServe(ctx context.Context) error {
	if s.Addr == "" {
		return nil
	}
	if s.Snapshot == nil {
		return fmt.Errorf("web UI snapshot function is required")
	}

	server := &http.Server{
		Addr:              s.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		log.Printf("%s web UI listening on http://%s", s.Title, displayAddr(s.Addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func (s Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	contents, err := distFiles.ReadFile("dist/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(contents)
}

func (s Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == r.URL.Path || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	http.ServeFileFS(w, r, distFiles, "dist/"+name)
}

func (s Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s.Snapshot()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if s.Logs == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.Logs())
}

func (s Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	if s.Capture == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.Capture())
}

func (s Server) handleAPI(method string, handler APIHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handler == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != method {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64*1024))
		if err != nil {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		result, err := handler(r.Context(), json.RawMessage(payload))
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, result)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if value == nil {
		value = map[string]bool{"ok": true}
	}
	if err := encoder.Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeAPIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := err.Error()
	var apiErr APIError
	if errors.As(err, &apiErr) {
		status = apiErr.Status
		if status == 0 {
			status = http.StatusInternalServerError
		}
		message = apiErr.Message
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
			w.Header().Set("WWW-Authenticate", `Bearer realm="anylan"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
