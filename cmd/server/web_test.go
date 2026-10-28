package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithWebAssets(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("app shell"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "assets", "app.js"), []byte("app bundle"), 0o600); err != nil {
		t.Fatal(err)
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-API-Path", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	handler, err := withWebAssets(api, directory)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		method      string
		path        string
		status      int
		body        string
		apiPath     string
		contentType string
	}{
		{name: "root document", method: http.MethodGet, path: "/", status: http.StatusOK, body: "app shell", contentType: "text/html"},
		{name: "client route fallback", method: http.MethodGet, path: "/room/abc123", status: http.StatusOK, body: "app shell", contentType: "text/html"},
		{name: "static asset", method: http.MethodGet, path: "/assets/app.js", status: http.StatusOK, body: "app bundle", contentType: "text/javascript"},
		{name: "missing asset", method: http.MethodGet, path: "/assets/missing.js", status: http.StatusNotFound},
		{name: "health routed to api", method: http.MethodGet, path: "/health", status: http.StatusNoContent, apiPath: "/health"},
		{name: "api route", method: http.MethodGet, path: "/api/v1/rooms", status: http.StatusNoContent, apiPath: "/api/v1/rooms"},
		{name: "non read static method routed to api", method: http.MethodPost, path: "/room/abc123", status: http.StatusNoContent, apiPath: "/room/abc123"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, test.status, response.Body.String())
			}
			if test.body != "" && response.Body.String() != test.body {
				t.Errorf("body = %q, want %q", response.Body.String(), test.body)
			}
			if test.apiPath != "" && response.Header().Get("X-API-Path") != test.apiPath {
				t.Errorf("API path = %q, want %q", response.Header().Get("X-API-Path"), test.apiPath)
			}
			if test.contentType != "" && !strings.Contains(response.Header().Get("Content-Type"), test.contentType) {
				t.Errorf("Content-Type = %q, want to contain %q", response.Header().Get("Content-Type"), test.contentType)
			}
		})
	}
}

func TestWithWebAssetsOptional(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("api only")) })
	handler, err := withWebAssets(api, "")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Body.String() != "api only" {
		t.Fatalf("body = %q, want API response", response.Body.String())
	}
}

func TestWithWebAssetsRequiresIndex(t *testing.T) {
	_, err := withWebAssets(http.NotFoundHandler(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "index.html") {
		t.Fatalf("error = %v, want missing index.html error", err)
	}
}

func TestDefaultListenAddress(t *testing.T) {
	tests := []struct {
		name string
		port string
		want string
	}{
		{name: "local default", want: "127.0.0.1:8080"},
		{name: "platform port", port: "10000", want: "0.0.0.0:10000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PORT", test.port)
			if got := defaultListenAddress(); got != test.want {
				t.Fatalf("defaultListenAddress() = %q, want %q", got, test.want)
			}
		})
	}
}
