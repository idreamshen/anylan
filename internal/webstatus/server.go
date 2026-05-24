package webstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"time"
)

type Server struct {
	Addr     string
	Title    string
	Snapshot func() any
}

func (s Server) ListenAndServe(ctx context.Context) error {
	if s.Addr == "" {
		return nil
	}
	if s.Snapshot == nil {
		return fmt.Errorf("web status snapshot function is required")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/status", s.handleStatus)

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
		log.Printf("%s web status listening on http://%s", s.Title, displayAddr(s.Addr))
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
	if err := pageTemplate.Execute(w, pageData{Title: s.Title}); err != nil {
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

var pageTemplate = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root {
  color-scheme: light dark;
  font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  background: #f7f7f4;
  color: #20231f;
}
body {
  margin: 0;
}
main {
  max-width: 1180px;
  margin: 0 auto;
  padding: 28px 20px 48px;
}
header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 18px;
  margin-bottom: 22px;
}
h1 {
  font-size: 28px;
  line-height: 1.15;
  margin: 0;
}
.muted {
  color: #686d62;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 12px;
  margin-bottom: 18px;
}
.metric, table {
  border: 1px solid #deded6;
  background: #ffffff;
  border-radius: 8px;
}
.metric {
  padding: 14px;
}
.label {
  font-size: 12px;
  text-transform: uppercase;
  color: #686d62;
}
.value {
  margin-top: 6px;
  font-size: 20px;
  font-weight: 650;
  overflow-wrap: anywhere;
}
section {
  margin-top: 22px;
}
h2 {
  font-size: 18px;
  margin: 0 0 10px;
}
table {
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
  overflow: hidden;
}
th, td {
  text-align: left;
  padding: 10px 12px;
  border-bottom: 1px solid #ededE7;
  font-size: 14px;
  white-space: nowrap;
}
th {
  font-size: 12px;
  text-transform: uppercase;
  color: #686d62;
  background: #fafaf7;
}
tr:last-child td {
  border-bottom: 0;
}
pre {
  margin: 0;
  padding: 14px;
  background: #20231f;
  color: #f7f7f4;
  border-radius: 8px;
  overflow: auto;
}
@media (max-width: 720px) {
  header {
    display: block;
  }
  .table-wrap {
    overflow-x: auto;
  }
}
@media (prefers-color-scheme: dark) {
  :root {
    background: #151713;
    color: #f2f2ec;
  }
  .muted, .label, th {
    color: #a7ad9e;
  }
  .metric, table {
    background: #1f221d;
    border-color: #383d32;
  }
  th {
    background: #252920;
  }
  th, td {
    border-bottom-color: #33372f;
  }
}
</style>
</head>
<body>
<main>
  <header>
    <h1>{{.Title}}</h1>
    <div id="updated" class="muted"></div>
  </header>
  <div id="app"></div>
</main>
<script>
const app = document.getElementById("app");
const updated = document.getElementById("updated");
let previous = null;
let previousAt = 0;

function bytes(value) {
  if (!Number.isFinite(value)) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let next = value;
  let unit = 0;
  while (next >= 1024 && unit < units.length - 1) {
    next /= 1024;
    unit += 1;
  }
  return next.toFixed(unit === 0 ? 0 : 1) + " " + units[unit];
}

function rate(current, path) {
  const now = Date.now();
  const currentValue = path.reduce((obj, key) => obj && obj[key], current) || 0;
  const previousValue = previous ? path.reduce((obj, key) => obj && obj[key], previous) || 0 : currentValue;
  const seconds = Math.max((now - previousAt) / 1000, 1);
  return Math.max(0, (currentValue - previousValue) / seconds);
}

function metric(label, value) {
  return "<div class=\"metric\"><div class=\"label\">" + label + "</div><div class=\"value\">" + value + "</div></div>";
}

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" }[c]));
}

function render(data) {
  if (Array.isArray(data.rooms)) {
    renderServer(data);
  } else {
    renderClient(data);
  }
  updated.textContent = "Updated " + new Date().toLocaleTimeString();
}

function renderServer(data) {
  const peers = data.rooms.flatMap(room => (room.peers || []).map(peer => ({ room, peer })));
  const rx = peers.reduce((sum, item) => sum + (item.peer.rx_bytes || 0), 0);
  const tx = peers.reduce((sum, item) => sum + (item.peer.tx_bytes || 0), 0);
  const rows = peers.map(({ room, peer }) => "<tr>" +
    "<td>" + esc(room.name) + "</td>" +
    "<td>" + esc(peer.display_name || peer.id) + "</td>" +
    "<td>" + esc(peer.ip) + "</td>" +
    "<td>" + esc(peer.mac) + "</td>" +
    "<td>" + bytes(peer.rx_bytes || 0) + "</td>" +
    "<td>" + bytes(peer.tx_bytes || 0) + "</td>" +
  "</tr>").join("");
  app.innerHTML =
    "<div class=\"grid\">" +
      metric("Rooms", data.rooms.length) +
      metric("Peers", peers.length) +
      metric("RX total", bytes(rx)) +
      metric("TX total", bytes(tx)) +
    "</div>" +
    "<section>" +
      "<h2>Peers</h2>" +
      "<div class=\"table-wrap\"><table>" +
        "<thead><tr><th>Room</th><th>Name / ID</th><th>IP</th><th>MAC</th><th>RX</th><th>TX</th></tr></thead>" +
        "<tbody>" + (rows || "<tr><td colspan=\"6\" class=\"muted\">No active peers</td></tr>") + "</tbody>" +
      "</table></div>" +
    "</section>";
}

function renderClient(data) {
  app.innerHTML =
    "<div class=\"grid\">" +
      metric("State", esc(data.state || "unknown")) +
      metric("Room", esc(data.room || "-")) +
      metric("Virtual IP", esc(data.ipv4 || "-")) +
      metric("RX rate", bytes(rate(data, ["rx_bytes"])) + "/s") +
      metric("TX rate", bytes(rate(data, ["tx_bytes"])) + "/s") +
      metric("Reconnects", data.reconnects || 0) +
    "</div>" +
    "<section>" +
      "<h2>Session</h2>" +
      "<div class=\"table-wrap\"><table><tbody>" +
        "<tr><th>Server</th><td>" + esc(data.server || "-") + "</td></tr>" +
        "<tr><th>Device</th><td>" + esc(data.device || "-") + "</td></tr>" +
        "<tr><th>Peer ID</th><td>" + esc(data.peer_id || "-") + "</td></tr>" +
        "<tr><th>CIDR</th><td>" + esc(data.cidr || "-") + "</td></tr>" +
        "<tr><th>MAC</th><td>" + esc(data.mac || "-") + "</td></tr>" +
        "<tr><th>MTU</th><td>" + esc(data.mtu || "-") + "</td></tr>" +
        "<tr><th>RX total</th><td>" + bytes(data.rx_bytes || 0) + "</td></tr>" +
        "<tr><th>TX total</th><td>" + bytes(data.tx_bytes || 0) + "</td></tr>" +
        "<tr><th>Last error</th><td>" + esc(data.last_error || "") + "</td></tr>" +
      "</tbody></table></div>" +
    "</section>";
}

async function refresh() {
  try {
    const res = await fetch("/api/status", { cache: "no-store" });
    const data = await res.json();
    render(data);
    previous = data;
    previousAt = Date.now();
  } catch (err) {
    app.innerHTML = "<pre>" + esc(err) + "</pre>";
  }
}

refresh();
setInterval(refresh, 1000);
</script>
</body>
</html>`))
