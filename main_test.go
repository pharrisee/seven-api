package main

import (
	"os"
	"path/filepath"
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
