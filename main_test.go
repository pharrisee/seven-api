package main

import "testing"

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
