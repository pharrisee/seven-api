package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAPIURL(t *testing.T) {
	tests := []struct {
		name    string
		apiURL  string
		wantErr bool
	}{
		{name: "localhost HTTP", apiURL: "http://localhost:9898"},
		{name: "IPv4 loopback HTTP", apiURL: "http://127.0.0.1:9898"},
		{name: "IPv6 loopback HTTP", apiURL: "http://[::1]:9898"},
		{name: "remote HTTPS", apiURL: "https://api.example.com"},
		{name: "remote HTTP", apiURL: "http://api.example.com", wantErr: true},
		{name: "unsupported scheme", apiURL: "ftp://localhost", wantErr: true},
		{name: "missing hostname", apiURL: "https:///api", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAPIURL(tt.apiURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateAPIURL(%q) error = %v, wantErr %t", tt.apiURL, err, tt.wantErr)
			}
		})
	}
}

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		name      string
		origin    string
		fetchSite string
		want      bool
	}{
		{name: "no origin header", want: true},
		{name: "same origin", origin: "http://example.com", want: true},
		{name: "cross origin", origin: "http://evil.example", want: false},
		{name: "cross-site fetch", fetchSite: "cross-site", want: false},
		{name: "invalid origin", origin: "://bad", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://example.com/x", nil)
			request.Host = "example.com"
			if tt.origin != "" {
				request.Header.Set("Origin", tt.origin)
			}
			if tt.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", tt.fetchSite)
			}
			if got := sameOrigin(request); got != tt.want {
				t.Fatalf("sameOrigin() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestValidSpecFile(t *testing.T) {
	valid := []string{"openapi.yaml", "Commands.openapi.yaml", "a.b.yaml"}
	for _, name := range valid {
		if !validSpecFile(name) {
			t.Errorf("validSpecFile(%q) = false, want true", name)
		}
	}

	invalid := []string{"", ".", "..", "../x.yaml", "sub/x.yaml", `sub\x.yaml`, "/abs.yaml"}
	for _, name := range invalid {
		if validSpecFile(name) {
			t.Errorf("validSpecFile(%q) = true, want false", name)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := recorder.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP still allows inline styles: %q", csp)
	}
	for name, want := range map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"Referrer-Policy":              "no-referrer",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
	} {
		if got := recorder.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := recorder.Header().Get("Permissions-Policy"); got == "" {
		t.Error("Permissions-Policy header is missing")
	}
}

func TestReloadKeepsLastGoodSpec(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "openapi.yaml")
	valid := []byte("openapi: 3.0.0\ninfo: {title: reload-test, version: \"1\"}\npaths: {}\n")
	if err := os.WriteFile(specPath, valid, 0o600); err != nil {
		t.Fatal(err)
	}

	oldDir := *localDir
	*localDir = dir
	defer func() { *localDir = oldDir }()

	specMu.Lock()
	oldData, oldErr := specData, specErr
	specData, specErr = nil, nil
	specMu.Unlock()
	defer func() {
		specMu.Lock()
		specData, specErr = oldData, oldErr
		specMu.Unlock()
	}()

	if _, err := reloadSpec(); err != nil {
		t.Fatalf("initial reload: %v", err)
	}
	before, err := currentSpec()
	if err != nil {
		t.Fatalf("currentSpec after good reload: %v", err)
	}

	if err := os.WriteFile(specPath, []byte("paths: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reloadSpec(); err == nil {
		t.Fatal("expected reload to fail with an invalid spec")
	}

	after, err := currentSpec()
	if err != nil {
		t.Fatalf("last good spec was discarded: %v", err)
	}
	if after.Title() != before.Title() {
		t.Fatalf("served spec changed: got %q, want %q", after.Title(), before.Title())
	}
}
