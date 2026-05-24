package webui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"time"
)

//go:embed static/*
var staticFiles embed.FS

var indexTemplate = template.Must(template.ParseFS(staticFiles, "static/index.html"))

type Server struct {
	Addr     string
	Title    string
	Token    string
	Snapshot func() any
}

func (s Server) ListenAndServe(ctx context.Context) error {
	if s.Addr == "" {
		return nil
	}
	if s.Snapshot == nil {
		return fmt.Errorf("web UI snapshot function is required")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.withAuth(s.handleIndex))
	mux.HandleFunc("/api/status", s.withAuth(s.handleStatus))
	mux.Handle("/static/", s.withAuth(http.FileServer(http.FS(staticFiles)).ServeHTTP))

	server := &http.Server{
		Addr:              s.Addr,
		Handler:           mux,
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
	if err := indexTemplate.Execute(w, pageData{Title: s.Title}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(s.Snapshot()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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

type pageData struct {
	Title string
}
