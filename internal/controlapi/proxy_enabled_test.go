package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"open-mihomo-gateway/internal/config"
)

func TestProxyEnabledDefaultsOnAndPersistsOff(t *testing.T) {
	server := newTestServer(t)
	var patched []string
	var closed int
	server.fetchTrafficMode = func(context.Context, config.Config) (string, error) {
		return "rule", nil
	}
	server.patchTrafficMode = func(_ context.Context, _ config.Config, mode string) error {
		patched = append(patched, mode)
		return nil
	}
	server.closeAllConnections = func(context.Context, config.Config) error {
		closed++
		return nil
	}

	response := performAuthorized(server, http.MethodGet, "/api/v1/proxy-enabled", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
	}
	var fetched ProxyEnabledResponse
	if err := json.Unmarshal(response.Body.Bytes(), &fetched); err != nil {
		t.Fatal(err)
	}
	if !fetched.Enabled || fetched.Mode != "rule" || !fetched.Live {
		t.Fatalf("GET default = %#v", fetched)
	}

	response = performAuthorized(server, http.MethodPost, "/api/v1/proxy-enabled", []byte(`{"enabled":false}`))
	if response.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", response.Code, response.Body.String())
	}
	var updated ProxyEnabledResponse
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.Mode != "direct" || !updated.Live || updated.Warning != "" {
		t.Fatalf("POST off = %#v", updated)
	}
	if strings.Join(patched, ",") != "direct" || closed != 1 {
		t.Fatalf("patched=%v closed=%d", patched, closed)
	}
	persisted, err := os.ReadFile(filepath.Join(server.store.Dir(), "proxy-enabled.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persisted), `"enabled": false`) {
		t.Fatalf("persisted=%s", persisted)
	}

	server.fetchTrafficMode = func(context.Context, config.Config) (string, error) {
		return "direct", nil
	}
	response = performAuthorized(server, http.MethodGet, "/api/v1/proxy-enabled", nil)
	if err := json.Unmarshal(response.Body.Bytes(), &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.Enabled || fetched.Mode != "direct" {
		t.Fatalf("GET after off = %#v", fetched)
	}
}

func TestProxyEnabledRemembersPreferenceWhenMihomoIsDown(t *testing.T) {
	server := newTestServer(t)
	server.fetchTrafficMode = func(context.Context, config.Config) (string, error) {
		return "", os.ErrNotExist
	}
	server.patchTrafficMode = func(context.Context, config.Config, string) error {
		return os.ErrNotExist
	}
	response := performAuthorized(server, http.MethodPost, "/api/v1/proxy-enabled", []byte(`{"enabled":false}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var updated ProxyEnabledResponse
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.Live || !strings.Contains(updated.Warning, "启动网关后") {
		t.Fatalf("POST while down = %#v", updated)
	}
	enabled, err := server.store.ProxyEnabled()
	if err != nil || enabled {
		t.Fatalf("stored enabled=%t err=%v", enabled, err)
	}
}

func TestApplyStoredProxyModeAfterStart(t *testing.T) {
	server := newTestServer(t)
	if err := server.store.SaveProxyEnabled(false); err != nil {
		t.Fatal(err)
	}
	var patched string
	var closed bool
	server.patchTrafficMode = func(_ context.Context, _ config.Config, mode string) error {
		patched = mode
		return nil
	}
	server.closeAllConnections = func(context.Context, config.Config) error {
		closed = true
		return nil
	}
	server.applyStoredProxyMode(context.Background())
	if patched != "direct" || !closed {
		t.Fatalf("patched=%q closed=%t", patched, closed)
	}
}
