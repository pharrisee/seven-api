package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pharrisee/seven-api/ui"
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

// useTestSpec swaps in a minimal spec and API base URL for handler tests.
func useTestSpec(t *testing.T, baseURL string) {
	t.Helper()

	spec := map[string]any{
		"openapi": "3.0.0",
		"info":    map[string]any{"title": "test", "version": "1"},
		"paths": map[string]any{
			"/api/player": map[string]any{"get": map[string]any{
				"responses": map[string]any{"200": map[string]any{"description": "ok"}},
			}},
			"/api/x": map[string]any{"post": map[string]any{
				"responses": map[string]any{"200": map[string]any{"description": "ok"}},
			}},
		},
	}

	specMu.Lock()
	oldData, oldAt, oldErr := specData, specLoadedAt, specErr
	specData, specLoadedAt, specErr = spec, time.Now(), nil
	specMu.Unlock()

	oldURL := *apiURL
	*apiURL = baseURL

	t.Cleanup(func() {
		specMu.Lock()
		specData, specLoadedAt, specErr = oldData, oldAt, oldErr
		specMu.Unlock()
		*apiURL = oldURL
	})
}

func TestHandleProxyRejectsUndeclaredOperations(t *testing.T) {
	useTestSpec(t, "http://127.0.0.1:1")

	body := strings.NewReader(`{"method":"GET","path":"/api/nope"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/proxy", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handleProxy(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestHandleProxyRequiresJSONContentType(t *testing.T) {
	useTestSpec(t, "http://127.0.0.1:1")

	request := httptest.NewRequest(http.MethodPost, "/api/proxy", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "text/plain")
	recorder := httptest.NewRecorder()
	handleProxy(recorder, request)

	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnsupportedMediaType)
	}
}

func TestHandleProxyForwardsRequest(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotSecret string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		gotSecret = r.Header.Get("X-SDTD-API-SECRET")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	useTestSpec(t, upstream.URL)

	body := strings.NewReader(`{"method":"GET","path":"/api/player","query":"a=1","headers":{"X-SDTD-API-SECRET":"s3cret"}}`)
	request := httptest.NewRequest(http.MethodPost, "/api/proxy", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handleProxy(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/player" || gotQuery != "a=1" {
		t.Fatalf("upstream got %s %s?%s", gotMethod, gotPath, gotQuery)
	}
	if gotSecret != "s3cret" {
		t.Fatalf("upstream secret header = %q, want s3cret", gotSecret)
	}

	var result ui.ProxyResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decoding proxy result: %v", err)
	}
	if result.Status != http.StatusOK || result.Body != `{"ok":true}` {
		t.Fatalf("unexpected proxy result: %+v", result)
	}
}

func TestHandleUITryItTranslatesAuth(t *testing.T) {
	var gotCookie, gotName, gotSecret string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotName = r.Header.Get("X-SDTD-API-TOKENNAME")
		gotSecret = r.Header.Get("X-SDTD-API-SECRET")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	useTestSpec(t, upstream.URL)

	form := url.Values{
		"method": {"POST"},
		"path":   {"/api/x"},
		"body":   {`{"a":1}`},
	}
	request := httptest.NewRequest(http.MethodPost, "/ui/tryit", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-SDTD-API-TOKENNAME", "token")
	request.Header.Set("X-SDTD-API-SECRET", "secret")
	request.Header.Set("X-SDTD-SESSION", "session=abc")
	recorder := httptest.NewRecorder()
	handleUITryIt(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if gotName != "token" || gotSecret != "secret" {
		t.Fatalf("upstream auth headers = %q/%q", gotName, gotSecret)
	}
	if gotCookie != "session=abc" {
		t.Fatalf("upstream Cookie = %q, want session=abc", gotCookie)
	}

	html := recorder.Body.String()
	for _, want := range []string{`200 200 OK`, `data-copy-curl="1"`, `curl -sS -X POST`} {
		if !strings.Contains(html, want) {
			t.Errorf("try-it response is missing %q", want)
		}
	}
}

func TestCurlCommand(t *testing.T) {
	got := curlCommand("POST", "http://localhost:9898/api/x",
		url.Values{"a": {"1"}, "b": {"x y"}},
		map[string]string{"X-Test": "it's", "Content-Type": "application/json"},
		`{"a":1}`)
	want := `curl -sS -X POST -H 'Content-Type: application/json' -H 'X-Test: it'\''s' ` +
		`--data-raw '{"a":1}' 'http://localhost:9898/api/x?a=1&b=x+y'`
	if got != want {
		t.Fatalf("curlCommand() =\n%s\nwant\n%s", got, want)
	}
}

func TestHealthz(t *testing.T) {
	specMu.Lock()
	oldData, oldAt := specData, specLoadedAt
	specData, specLoadedAt = map[string]any{}, time.Now()
	specMu.Unlock()
	defer func() {
		specMu.Lock()
		specData, specLoadedAt = oldData, oldAt
		specMu.Unlock()
	}()

	recorder := httptest.NewRecorder()
	handleHealthz(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var payload struct {
		Status     string `json:"status"`
		Version    string `json:"version"`
		SpecLoaded bool   `json:"specLoaded"`
		LoadedAt   string `json:"specLoadedAt"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decoding health payload: %v", err)
	}
	if payload.Status != "ok" || !payload.SpecLoaded {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Version == "" {
		t.Error("version is empty")
	}
	if payload.LoadedAt == "" {
		t.Error("specLoadedAt is empty")
	}
}

func TestHealthzRejectsWrites(t *testing.T) {
	recorder := httptest.NewRecorder()
	handleHealthz(recorder, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestBuildVersion(t *testing.T) {
	if got := buildVersion(); got == "" {
		t.Fatal("buildVersion() returned an empty string")
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
	oldData, oldAt, oldErr := specData, specLoadedAt, specErr
	specData, specLoadedAt, specErr = nil, time.Time{}, nil
	specMu.Unlock()
	defer func() {
		specMu.Lock()
		specData, specLoadedAt, specErr = oldData, oldAt, oldErr
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
