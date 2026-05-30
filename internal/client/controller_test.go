package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

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
