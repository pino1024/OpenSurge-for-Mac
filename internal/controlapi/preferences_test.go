package controlapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIPreferencesDefaultToSystemAndPersistLanguage(t *testing.T) {
	server := newTestServer(t)

	response := performAuthorized(server, http.MethodGet, "/api/v1/ui-preferences", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("default preferences status=%d body=%s", response.Code, response.Body.String())
	}
	var preferences UIPreferences
	if err := json.Unmarshal(response.Body.Bytes(), &preferences); err != nil {
		t.Fatal(err)
	}
	if preferences.Language != UILanguageSystem {
		t.Fatalf("default language=%q", preferences.Language)
	}

	response = performAuthorized(server, http.MethodPut, "/api/v1/ui-preferences", []byte(`{"language":"en"}`))
	if response.Code != http.StatusOK {
		t.Fatalf("save preferences status=%d body=%s", response.Code, response.Body.String())
	}
	persisted, err := os.ReadFile(filepath.Join(server.store.Dir(), "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persisted), `"language": "en"`) {
		t.Fatalf("persisted preferences=%s", persisted)
	}
	persistedInfo, err := os.Stat(filepath.Join(server.store.Dir(), "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := persistedInfo.Mode().Perm(); mode != 0o600 {
		t.Fatalf("preferences mode=%#o", mode)
	}

	overview := performAuthorized(server, http.MethodGet, "/api/v1/overview", nil)
	want := `"ui_preferences":{"schema_version":1,"language":"en","menu_bar":{"download":true,"cpu":true,"memory":true,"temperature":true}}`
	if overview.Code != http.StatusOK || !strings.Contains(overview.Body.String(), want) {
		t.Fatalf("overview preferences status=%d body=%s", overview.Code, overview.Body.String())
	}
	menuBar := performAuthorized(server, http.MethodGet, "/api/v1/menubar", nil)
	if menuBar.Code != http.StatusOK || !strings.Contains(menuBar.Body.String(), want) {
		t.Fatalf("menubar preferences status=%d body=%s", menuBar.Code, menuBar.Body.String())
	}
}

func TestUIPreferencesMergeMenuBarWithoutResettingLanguage(t *testing.T) {
	server := newTestServer(t)
	performAuthorized(server, http.MethodPut, "/api/v1/ui-preferences", []byte(`{"language":"en"}`))
	response := performAuthorized(server, http.MethodPut, "/api/v1/ui-preferences", []byte(`{"menu_bar":{"cpu":false}}`))
	if response.Code != http.StatusOK {
		t.Fatalf("save menu bar status=%d body=%s", response.Code, response.Body.String())
	}
	var preferences UIPreferences
	if err := json.Unmarshal(response.Body.Bytes(), &preferences); err != nil {
		t.Fatal(err)
	}
	if preferences.Language != UILanguageEN || !preferences.MenuBar.Download || preferences.MenuBar.CPU || !preferences.MenuBar.Memory || !preferences.MenuBar.Temperature {
		t.Fatalf("merged preferences=%+v", preferences)
	}
}

func TestUIPreferencesDefaultMemoryWhenOmitted(t *testing.T) {
	server := newTestServer(t)
	path := filepath.Join(server.store.Dir(), "preferences.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"language":"en","menu_bar":{"download":true,"cpu":true,"temperature":false}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	preferences, err := server.store.UIPreferences()
	if err != nil {
		t.Fatal(err)
	}
	if !preferences.MenuBar.Download || !preferences.MenuBar.CPU || !preferences.MenuBar.Memory || preferences.MenuBar.Temperature {
		t.Fatalf("omitted memory should default on: %+v", preferences.MenuBar)
	}
}

func TestUIPreferencesRejectUnsupportedLanguage(t *testing.T) {
	server := newTestServer(t)
	response := performAuthorized(server, http.MethodPut, "/api/v1/ui-preferences", []byte(`{"language":"fr"}`))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "ui_language_unsupported") {
		t.Fatalf("unsupported language status=%d body=%s", response.Code, response.Body.String())
	}
}
