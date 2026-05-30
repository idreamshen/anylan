package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

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
