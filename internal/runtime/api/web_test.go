package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	for _, route := range []string{"/", "/capsules/afr_fixture", "/jobs/job_fixture"} {
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
