package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/idreamshen/anylan/internal/protocol"
	"github.com/idreamshen/anylan/internal/webui"
)

func TestControllerRejectsInvalidJoin(t *testing.T) {
	controller := NewController(context.Background())

	_, err := controller.Join(context.Background(), json.RawMessage(`{"room":"room"}`))
	if err == nil {
		t.Fatal("Join accepted missing server")
	}
	var apiErr webui.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("Join error = %T %v, want bad request APIError", err, err)
	}
}

func TestControllerLeaveWithoutSessionIsNoop(t *testing.T) {
	controller := NewController(context.Background())

	result, err := controller.Leave(context.Background(), nil)
	if err != nil {
		t.Fatalf("Leave failed: %v", err)
	}
	snap, ok := result.(Snapshot)
	if !ok {
		t.Fatalf("Leave result = %T, want Snapshot", result)
	}
	if snap.State != "idle" {
		t.Fatalf("state = %q, want idle", snap.State)
	}
}

func TestNormalizeConfigTrimsAndDefaults(t *testing.T) {
	cfg := Config{Server: " example:4433 ", Room: " room ", DisplayName: " player "}
	if err := normalizeConfig(&cfg); err != nil {
		t.Fatalf("normalizeConfig failed: %v", err)
	}
	if cfg.Server != "example:4433" || cfg.Room != "room" || cfg.DisplayName != "player" {
		t.Fatalf("trimmed config = %+v", cfg)
	}
	if cfg.DeviceName == "" {
		t.Fatal("DeviceName was not defaulted")
	}
}

func TestControllerDefaultsPrioritizeVirtualAdapter(t *testing.T) {
	controller := NewControllerWithConfig(context.Background(), filepath.Join(t.TempDir(), "missing", "client.json"))

	snap := controller.Snapshot()
	if !snap.PrioritizeVirtualAdapter {
		t.Fatal("PrioritizeVirtualAdapter default = false, want true")
	}
}

func TestControllerLoadsLocalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "client.json")
	prioritize := false
	stored := localClientConfig{
		Server:                   "server.example:4433",
		Room:                     "room-a",
		DisplayName:              "alice",
		DeviceName:               "tap-test",
		InsecureSkipVerify:       true,
		PrioritizeVirtualAdapter: &prioritize,
	}
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	controller := NewControllerWithConfig(context.Background(), path)
	snap := controller.Snapshot()
	if snap.Server != stored.Server || snap.Room != stored.Room || snap.DisplayName != stored.DisplayName || snap.DeviceName != stored.DeviceName {
		t.Fatalf("loaded snapshot = %+v", snap)
	}
	if !snap.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify was not loaded")
	}
	if snap.PrioritizeVirtualAdapter {
		t.Fatal("PrioritizeVirtualAdapter = true, want loaded false")
	}
}

func TestSaveAndLoadClientConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "client.json")
	cfg := Config{
		Server:                   "server.example:4433",
		Room:                     "room-a",
		DisplayName:              "alice",
		DeviceName:               "tap-test",
		InsecureSkipVerify:       true,
		PrioritizeVirtualAdapter: false,
	}
	if err := saveClientConfig(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	loaded, err := loadClientConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.Server != cfg.Server || loaded.Room != cfg.Room || loaded.DisplayName != cfg.DisplayName || loaded.DeviceName != cfg.DeviceName {
		t.Fatalf("loaded config = %+v", loaded)
	}
	if !loaded.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify was not preserved")
	}
	if loaded.PrioritizeVirtualAdapter {
		t.Fatal("PrioritizeVirtualAdapter = true, want saved false")
	}
}

func TestControllerJoinSavesLocalConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "client.json")
	controller := NewControllerWithConfig(ctx, path)
	payload := json.RawMessage(`{
		"server":"127.0.0.1:1",
		"room":"room-a",
		"display_name":"alice",
		"device_name":"tap-test",
		"insecure_skip_verify":true,
		"prioritize_virtual_adapter":false
	}`)

	if _, err := controller.Join(context.Background(), payload); err != nil {
		t.Fatalf("Join failed: %v", err)
	}
	_, _ = controller.Leave(context.Background(), nil)
	loaded, err := loadClientConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.Server != "127.0.0.1:1" || loaded.Room != "room-a" || loaded.DisplayName != "alice" || loaded.DeviceName != "tap-test" {
		t.Fatalf("loaded config = %+v", loaded)
	}
	if !loaded.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify was not saved")
	}
	if loaded.PrioritizeVirtualAdapter {
		t.Fatal("PrioritizeVirtualAdapter = true, want saved false")
	}
}

func TestConfigFromJoinRequestDefaultsWebUITLS(t *testing.T) {
	req := JoinRequest{Server: "example:4433", Room: "room", InsecureSkipVerify: true, PrioritizeVirtualAdapter: true}
	cfg := configFromJoinRequest(req)
	if !cfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify was not copied from WebUI request")
	}
	if !cfg.PrioritizeVirtualAdapter {
		t.Fatal("PrioritizeVirtualAdapter was not copied from WebUI request")
	}
}

func TestSessionLogSnapshotKeepsCountersAndPeer(t *testing.T) {
	status := newStatus(Config{Server: "example:4433", Room: "room", DisplayName: "alice", DeviceName: "anylan0"})
	status.peerID = "peer-a"
	status.ipv4 = "10.240.0.2"
	status.mac = "02:00:00:00:00:01"
	status.joinedAt = time.Now()
	status.recordRx(42)
	status.recordTx(98)

	snap := status.sessionLogSnapshot()
	if snap.Server != "example:4433" || snap.Room != "room" || snap.PeerID != "peer-a" {
		t.Fatalf("unexpected snapshot identity: %#v", snap)
	}
	if snap.RxFrames != 1 || snap.RxBytes != 42 || snap.TxFrames != 1 || snap.TxBytes != 98 {
		t.Fatalf("unexpected snapshot counters: %#v", snap)
	}
}

func TestStatusRecordsPeerLatency(t *testing.T) {
	status := newStatus(Config{Server: "example:4433", Room: "room"})
	status.joined(protocol.JoinAccept{
		PeerID: "peer-a",
		Peers: []protocol.PeerInfo{
			{ID: "peer-a", IPv4: "10.240.0.2", MAC: "02:00:00:00:00:01", Features: []string{protocol.FeaturePeerLatency}},
			{ID: "peer-b", IPv4: "10.240.0.3", MAC: "02:00:00:00:00:02", Features: []string{protocol.FeaturePeerLatency}},
		},
	})
	now := time.Now()
	probe, ok := status.nextLatencyProbe(now)
	if !ok || probe.PeerID != "peer-b" || probe.ID == "" {
		t.Fatalf("probe = %#v ok=%v, want peer-b", probe, ok)
	}
	status.recordLatencyPong(protocol.PeerPong{ID: probe.ID, FromPeerID: "peer-b", ToPeerID: "peer-a"}, now.Add(25*time.Millisecond))

	snap := status.Snapshot()
	if len(snap.Peers) != 2 {
		t.Fatalf("peers = %d, want 2", len(snap.Peers))
	}
	if snap.Peers[1].LatencyMS == nil || *snap.Peers[1].LatencyMS != 25 {
		t.Fatalf("latency = %#v, want 25ms", snap.Peers[1].LatencyMS)
	}
	if snap.Peers[1].LatencyState != "ok" {
		t.Fatalf("latency state = %q, want ok", snap.Peers[1].LatencyState)
	}
}

func TestStatusExpiresLatencyProbe(t *testing.T) {
	status := newStatus(Config{Server: "example:4433", Room: "room"})
	status.joined(protocol.JoinAccept{
		PeerID: "peer-a",
		Peers: []protocol.PeerInfo{
			{ID: "peer-a", Features: []string{protocol.FeaturePeerLatency}},
			{ID: "peer-b", Features: []string{protocol.FeaturePeerLatency}},
		},
	})
	now := time.Now()
	if _, ok := status.nextLatencyProbe(now); !ok {
		t.Fatal("nextLatencyProbe returned false")
	}
	status.expireLatencyProbes(now.Add(peerLatencyProbeTimeout + time.Millisecond))

	snap := status.Snapshot()
	if snap.Peers[1].LatencyState != "timeout" {
		t.Fatalf("latency state = %q, want timeout", snap.Peers[1].LatencyState)
	}
}

func TestErrorStringNormalizesCommonSessionErrors(t *testing.T) {
	if got := errorString(nil); got != "closed" {
		t.Fatalf("nil error string = %q", got)
	}
	if got := errorString(context.Canceled); got != "cancelled" {
		t.Fatalf("cancel error string = %q", got)
	}
	if got := errorString(io.EOF); got != "eof" {
		t.Fatalf("eof error string = %q", got)
	}
}
