package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestInspectorHandlerServesStableRoutesWithoutMaskingAPI404s(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("flight inspector"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "assets", "app.js"), []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := inspectorHandler(directory)

	for _, route := range []string{"/", "/runs", "/runs/afr_fixture/understand", "/runs/afr_fixture/reproduce", "/runs/afr_fixture/compare/afr_candidate", "/capsules/afr_fixture", "/jobs", "/jobs/job_fixture"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "flight inspector") {
			t.Fatalf("stable route %s: status=%d body=%q", route, response.Code, response.Body.String())
		}
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if asset.Code != http.StatusOK || asset.Body.String() != "asset" {
		t.Fatalf("asset: status=%d body=%q", asset.Code, asset.Body.String())
	}
	for _, route := range []string{"/api", "/api/v1/unknown", "/v1", "/v1/unknown", "/assets/missing.js"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("API route %s was masked by inspector fallback: %d", route, response.Code)
		}
	}
}

func TestSessionAdvertisesLoopbackLocalModeWithoutCredentials(t *testing.T) {
	server := &Server{config: config.Config{Auth: config.AuthConfig{LocalBypass: true}}}
	response := httptest.NewRecorder()
	server.session(response, httptest.NewRequest(http.MethodGet, "/api/v1/session", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("session status = %d", response.Code)
	}
	var info types.SessionInfo
	if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !info.Connected || !info.LocalModeAvailable || info.AuthMode != "local_bypass" {
		t.Fatalf("session info = %#v", info)
	}
	if strings.Contains(response.Body.String(), "token") {
		t.Fatalf("local session response mentioned credentials: %s", response.Body.String())
	}
}
