package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
	g "maragu.dev/gomponents"

	"github.com/pharrisee/seven-api/ui"
)

//go:embed web/style.css web/app.js web/htmx.min.js
var webFS embed.FS

const (
	maxSpecSize    = 10 << 20
	maxProxyBody   = 8 << 20
	maxProxyResult = 5 << 20
)

var (
	addr        = flag.String("addr", "127.0.0.1:8080", "server listen address")
	apiURL      = flag.String("api-url", "http://localhost:9898", "7D2D server base URL")
	localDir    = flag.String("local-dir", "", "local directory for YAML files (overrides api-url)")
	showVersion = flag.Bool("version", false, "print the version and exit")
)

var (
	specData     map[string]any
	specLoadedAt time.Time
	specMu       sync.RWMutex
	specErr      error
)

var errSpecUnavailable = errors.New("specification unavailable")

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags)

	if *showVersion {
		fmt.Println(buildVersion())
		return
	}

	if loaded, err := reloadSpec(); err != nil {
		log.Printf("warning: initial spec load failed: %v", err)
		log.Printf("server will start but the viewer will show an error until reload succeeds")
	} else {
		log.Printf("spec loaded at %s", loaded.Format(time.RFC3339))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/op", handleIndex)
	mux.HandleFunc("/ui/op", handleUIOp)
	mux.HandleFunc("/ui/sidebar", handleUISidebar)
	mux.HandleFunc("/ui/toggle", handleUIToggle)
	mux.HandleFunc("/ui/tryit", handleUITryIt)
	mux.HandleFunc("/ui/reload", handleUIReload)
	mux.HandleFunc("/static/", handleStatic)
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/api/spec", handleSpec)
	mux.HandleFunc("/api/reload", handleReload)
	mux.HandleFunc("/api/config", handleConfig)
	mux.HandleFunc("/api/proxy", handleProxy)

	log.Printf("Server starting on %s", *addr)
	if *localDir != "" {
		log.Printf("  source: local dir %s", *localDir)
	} else {
		log.Printf("  source: %s", *apiURL)
	}
	logSpecInfo()

	server := &http.Server{
		Addr:              *addr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("fatal: %v", err)
		}
	case <-ctx.Done():
		log.Printf("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		<-serveErr
	}
}

// buildVersion reports the module version stamped into the binary, falling
// back to the VCS revision (or "(devel)" for a plain `go build`).
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(devel)"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	var revision string
	var modified bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return "(devel)"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		revision += "-dirty"
	}
	return revision
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	specMu.RLock()
	loaded := specData != nil
	loadedAt := specLoadedAt
	specMu.RUnlock()

	payload := map[string]any{
		"status":     "ok",
		"version":    buildVersion(),
		"specLoaded": loaded,
	}
	if !loadedAt.IsZero() {
		payload["specLoadedAt"] = loadedAt.Format(time.RFC3339)
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("encoding health: %v", err)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
				"connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		header.Set("Cross-Origin-Resource-Policy", "same-origin")
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func logSpecInfo() {
	specMu.RLock()
	defer specMu.RUnlock()
	if specErr != nil || specData == nil {
		return
	}
	if info, ok := specData["info"].(map[string]any); ok {
		if title, _ := info["title"].(string); title != "" {
			log.Printf("  title:  %s", title)
		}
		if version, _ := info["version"].(string); version != "" {
			log.Printf("  version: %s", version)
		}
	}
	if paths, ok := specData["paths"].(map[string]any); ok {
		log.Printf("  paths: %d", len(paths))
		log.Printf("  ops:  %d", countOperations(paths))
	}
}

// --- Pages ---

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/" && r.URL.Path != "/op" {
		http.NotFound(w, r)
		return
	}
	renderPage(w, r)
}

func renderPage(w http.ResponseWriter, r *http.Request) {
	spec, err := currentSpec()
	if err != nil {
		log.Printf("rendering error page: %v", err)
		renderNode(w, http.StatusServiceUnavailable,
			ui.ErrorPage("Failed to Load Spec", "The API specification could not be loaded. Check the server logs, then retry."))
		return
	}

	method, path, query := selection(r)
	state := ui.ReadState(r)
	if op, ok := spec.FindOperation(method, path); ok {
		changed := false
		for _, tag := range op.Tags {
			if _, seen := state.Tags[tag]; !seen {
				state.SetTag(tag, true)
				changed = true
			}
		}
		if changed {
			ui.WriteState(w, r, state)
		}
	}

	renderNode(w, http.StatusOK, ui.Page(spec, *apiURL, method, path, query, state, nil))
}

func handleUIOp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	spec, err := currentSpec()
	if err != nil {
		renderNode(w, http.StatusOK, ui.Notice("Specification unavailable. Reload to retry."))
		return
	}

	method, path, query := selection(r)
	state := ui.ReadState(r)
	if op, ok := spec.FindOperation(method, path); ok {
		changed := false
		for _, tag := range op.Tags {
			if _, seen := state.Tags[tag]; !seen {
				state.SetTag(tag, true)
				changed = true
			}
		}
		if changed {
			ui.WriteState(w, r, state)
		}
	}

	content := ui.ContentView(spec, method, path, state, nil)
	sidebar := ui.SidebarContent(spec, query, method, path, state, true)
	renderNode(w, http.StatusOK, g.Group{content, sidebar})
}

func handleUISidebar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	spec, err := currentSpec()
	if err != nil {
		renderNode(w, http.StatusOK, ui.SidebarNotice("Specification unavailable. Reload to retry."))
		return
	}
	method, path, query := selectionFromCurrentURL(r)
	renderNode(w, http.StatusOK, ui.SidebarInner(spec, query, method, path, ui.ReadState(r)))
}

func handleUIToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var request struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Open bool   `json:"open"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	request.Kind = strings.TrimSpace(request.Kind)
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len(request.Name) > 200 || strings.ContainsAny(request.Name, "\r\n") {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}

	state := ui.ReadState(r)
	switch request.Kind {
	case "tag":
		state.SetTag(request.Name, request.Open)
	case "section":
		state.SetSection(request.Name, request.Open)
	default:
		http.Error(w, "invalid kind", http.StatusBadRequest)
		return
	}
	ui.WriteState(w, r, state)
	w.WriteHeader(http.StatusNoContent)
}

func handleUIReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if _, err := reloadSpec(); err != nil {
		log.Printf("reload failed: %v", err)
		renderNode(w, http.StatusOK, ui.Notice("Reload failed. Check the server logs, then retry."))
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func renderNode(w http.ResponseWriter, status int, node g.Node) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := node.Render(w); err != nil {
		log.Printf("rendering response: %v", err)
	}
}

func selection(r *http.Request) (method, path, query string) {
	values := r.URL.Query()
	return values.Get("method"), values.Get("path"), values.Get("q")
}

func selectionFromCurrentURL(r *http.Request) (method, path, query string) {
	values := r.URL.Query()
	method, path = values.Get("method"), values.Get("path")
	query, hasQuery := values.Get("q"), values.Has("q")

	current := r.Header.Get("HX-Current-URL")
	if current == "" {
		return method, path, query
	}
	parsed, err := url.Parse(current)
	if err != nil {
		return method, path, query
	}
	currentValues := parsed.Query()
	if method == "" {
		method = currentValues.Get("method")
	}
	if path == "" {
		path = currentValues.Get("path")
	}
	if !hasQuery {
		query = currentValues.Get("q")
	}
	return method, path, query
}

// --- Static assets ---

func handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	file := strings.TrimPrefix(r.URL.Path, "/static/")
	clean := filepath.Clean(file)
	if !filepath.IsLocal(clean) || strings.Contains(clean, "\\") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	data, err := webFS.ReadFile("web/" + clean)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	etag := `"` + contentETag(data) + `"`
	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	if r.URL.Query().Get("v") != "" {
		// Versioned URLs are immutable; cache for a year without revalidating.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// Unversioned assets (e.g. htmx) may change; revalidate every time.
		w.Header().Set("Cache-Control", "no-cache")
	}
	switch {
	case strings.HasSuffix(clean, ".css"):
		w.Header().Set("Content-Type", "text/css")
	case strings.HasSuffix(clean, ".js"):
		w.Header().Set("Content-Type", "application/javascript")
	case strings.HasSuffix(clean, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(clean, ".png"):
		w.Header().Set("Content-Type", "image/png")
	case strings.HasSuffix(clean, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	// #nosec G705 -- data is read from the embedded filesystem, not from the request.
	if _, err := w.Write(data); err != nil {
		log.Printf("writing static asset: %v", err)
	}
}

// contentETag returns a short hex digest of the asset bytes for ETag matching.
func contentETag(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// etagMatches reports whether any of the validator values in If-None-Match
// (which may contain a star or a comma-separated list) matches the current ETag.
func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || part == etag {
			return true
		}
	}
	return false
}

// --- JSON API ---

func handleSpec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	specMu.RLock()
	snapshot := specData
	specMu.RUnlock()
	if snapshot == nil {
		http.Error(w, "specification unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		log.Printf("encoding spec: %v", err)
	}
}

func handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	loaded, err := reloadSpec()
	if err != nil {
		log.Printf("reload failed: %v", err)
		http.Error(w, "reload failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"loaded":  loaded.Format(time.RFC3339),
		"methods": countAllMethods(),
	}); err != nil {
		log.Printf("encoding reload response: %v", err)
	}
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"baseURL": *apiURL}); err != nil {
		log.Printf("encoding config: %v", err)
	}
}

// --- Proxy ---

func handleProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if err := validateAPIURL(*apiURL); err != nil {
		log.Printf("invalid api url: %v", err)
		http.Error(w, "proxy is not configured correctly", http.StatusInternalServerError)
		return
	}

	var request struct {
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Query   string            `json:"query"`
		Body    string            `json:"body"`
		Headers map[string]string `json:"headers"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxProxyBody))
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if !supportedMethod(method) {
		http.Error(w, "unsupported method", http.StatusBadRequest)
		return
	}
	path := strings.TrimSpace(request.Path)
	if path == "" || strings.ContainsAny(path, "\r\n\\") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	spec, err := currentSpec()
	if err != nil {
		http.Error(w, "specification unavailable", http.StatusServiceUnavailable)
		return
	}
	if !spec.Allows(method, path) {
		http.Error(w, "operation is not declared in the specification", http.StatusForbidden)
		return
	}

	result, err := doProxy(method, path, request.Query, request.Body, request.Headers)
	if err != nil {
		log.Printf("proxy request failed: %v", err)
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Printf("encoding proxy response: %v", err)
	}
}

func handleUITryIt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxProxyBody)
	if err := r.ParseForm(); err != nil {
		renderNode(w, http.StatusOK, ui.TryItResult(ui.ProxyResult{Error: "Invalid request."}, ""))
		return
	}

	method := strings.ToUpper(strings.TrimSpace(r.PostForm.Get("method")))
	pathTemplate := strings.TrimSpace(r.PostForm.Get("path"))
	if !supportedMethod(method) || pathTemplate == "" {
		renderNode(w, http.StatusOK, ui.TryItResult(ui.ProxyResult{Error: "Invalid operation."}, ""))
		return
	}

	spec, err := currentSpec()
	if err != nil {
		renderNode(w, http.StatusOK, ui.TryItResult(ui.ProxyResult{Error: "Specification unavailable."}, ""))
		return
	}
	if !spec.Allows(method, pathTemplate) {
		renderNode(w, http.StatusOK, ui.TryItResult(ui.ProxyResult{Error: "Operation is not declared in the specification."}, ""))
		return
	}

	finalPath := pathTemplate
	query := url.Values{}
	headers := map[string]string{}
	cookies := []string{}

	for key := range r.PostForm {
		if !strings.HasPrefix(key, "param:") {
			continue
		}
		parts := strings.SplitN(key, ":", 3)
		if len(parts) != 3 {
			continue
		}
		location, name := parts[1], parts[2]
		value := r.PostForm.Get(key)
		if value == "" {
			continue
		}
		switch location {
		case "path":
			finalPath = strings.ReplaceAll(finalPath, "{"+name+"}", url.PathEscape(value))
		case "query":
			query.Add(name, value)
		case "header":
			if !validHeaderName(name) || !validHeaderValue(value) {
				renderNode(w, http.StatusOK, ui.TryItResult(ui.ProxyResult{Error: "Invalid header parameter."}, ""))
				return
			}
			headers[name] = value
		case "cookie":
			if strings.ContainsAny(name, "=;\r\n") || strings.ContainsAny(value, ";\r\n") {
				renderNode(w, http.StatusOK, ui.TryItResult(ui.ProxyResult{Error: "Invalid cookie parameter."}, ""))
				return
			}
			cookies = append(cookies, name+"="+value)
		}
	}
	if strings.Contains(finalPath, "{") {
		renderNode(w, http.StatusOK, ui.TryItResult(ui.ProxyResult{Error: "Missing path parameter."}, ""))
		return
	}

	nameHeader, secretHeader := spec.AuthHeaders()
	if value := r.Header.Get(nameHeader); value != "" {
		headers[nameHeader] = value
	}
	if value := r.Header.Get(secretHeader); value != "" {
		headers[secretHeader] = value
	}
	if session := r.Header.Get("X-SDTD-SESSION"); session != "" {
		headers["Cookie"] = mergeCookie(headers["Cookie"], sessionCookie(session))
	}
	for _, cookie := range cookies {
		headers["Cookie"] = mergeCookie(headers["Cookie"], cookie)
	}

	body := r.PostForm.Get("body")
	if body != "" && headers["Content-Type"] == "" {
		contentType := r.PostForm.Get("content_type")
		if contentType == "" {
			contentType = "application/json"
		}
		headers["Content-Type"] = contentType
	}

	result, err := doProxy(method, finalPath, query.Encode(), body, headers)
	if err != nil {
		log.Printf("try-it request failed: %v", err)
		renderNode(w, http.StatusOK, ui.TryItResult(ui.ProxyResult{Error: "Upstream request failed. Check the server logs."}, ""))
		return
	}
	curl := curlCommand(method, strings.TrimRight(*apiURL, "/")+finalPath, query, headers, body)
	if r.Header.Get("HX-Request") == "" {
		// No-JavaScript fallback: render the full operation page with the
		// result already in the try-it panel.
		state := ui.ReadState(r)
		outcome := &ui.TryItOutcome{Result: result, Curl: curl}
		renderNode(w, http.StatusOK, ui.Page(spec, *apiURL, method, pathTemplate, "", state, outcome))
		return
	}
	renderNode(w, http.StatusOK, ui.TryItResult(result, curl))
}

// curlCommand renders the request the proxy just sent as a shell-safe cURL
// command, including the auth headers the browser supplied.
func curlCommand(method, rawURL string, query url.Values, headers map[string]string, body string) string {
	target := rawURL
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	parts := []string{"curl", "-sS", "-X", method}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, "-H", shellQuote(key+": "+headers[key]))
	}
	if body != "" {
		parts = append(parts, "--data-raw", shellQuote(body))
	}
	parts = append(parts, shellQuote(target))
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func doProxy(method, path, query, body string, headers map[string]string) (ui.ProxyResult, error) {
	if err := validateAPIURL(*apiURL); err != nil {
		return ui.ProxyResult{}, err
	}
	if !supportedMethod(method) {
		return ui.ProxyResult{}, errors.New("unsupported method")
	}
	if strings.ContainsAny(query, "\r\n") {
		return ui.ProxyResult{}, errors.New("invalid query")
	}

	base := strings.TrimRight(*apiURL, "/")
	target := base + path
	if query != "" {
		target += "?" + query
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}

	// #nosec G704 -- the target host is fixed by -api-url; path is allowlisted against the spec.
	request, err := http.NewRequest(method, target, bodyReader)
	if err != nil {
		return ui.ProxyResult{}, err
	}

	cookie := ""
	for key, value := range headers {
		if strings.EqualFold(key, "X-SDTD-SESSION") {
			cookie = mergeCookie(cookie, sessionCookie(value))
			continue
		}
		if !validHeaderName(key) || !validHeaderValue(value) {
			continue
		}
		switch strings.ToLower(key) {
		case "host", "content-length", "connection", "transfer-encoding", "upgrade", "proxy-connection", "te", "trailer":
			continue
		}
		if strings.EqualFold(key, "Cookie") {
			cookie = mergeCookie(cookie, value)
			continue
		}
		request.Header.Set(key, value)
	}
	if cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", "seven-api-viewer")
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	start := time.Now()
	// #nosec G704 -- request target is the fixed -api-url host.
	response, err := client.Do(request)
	if err != nil {
		return ui.ProxyResult{}, err
	}
	defer response.Body.Close()

	raw, err := readAllLimited(response.Body, maxProxyResult)
	if err != nil {
		return ui.ProxyResult{}, err
	}

	result := ui.ProxyResult{
		Status:     response.StatusCode,
		StatusText: response.Status,
		Headers:    response.Header,
		DurationMs: time.Since(start).Milliseconds(),
	}
	if utf8.Valid(raw) {
		result.Body = string(raw)
	} else {
		result.Body = base64.StdEncoding.EncodeToString(raw)
		result.Binary = true
	}
	return result, nil
}

func supportedMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

func validHeaderValue(value string) bool {
	for _, r := range value {
		if r == '\r' || r == '\n' || r == 0 {
			return false
		}
	}
	return true
}

func mergeCookie(existing, extra string) string {
	switch {
	case existing == "":
		return extra
	case extra == "":
		return existing
	default:
		return existing + "; " + extra
	}
}

func sessionCookie(value string) string {
	if strings.Contains(value, "=") {
		return value
	}
	return "sid=" + value
}

func readAllLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

// --- Spec loading ---

func reloadSpec() (time.Time, error) {
	spec, err := loadSpec()
	if err != nil {
		specMu.Lock()
		// Keep serving the last good spec. The error is only surfaced when
		// there is nothing usable to fall back to.
		if specData == nil {
			specErr = err
		}
		specMu.Unlock()
		return time.Time{}, err
	}

	loaded := time.Now()
	specMu.Lock()
	specData = spec
	specLoadedAt = loaded
	specErr = nil
	specMu.Unlock()
	return loaded, nil
}

func currentSpec() (*ui.Spec, error) {
	specMu.RLock()
	defer specMu.RUnlock()
	if specData == nil {
		return nil, errSpecUnavailable
	}
	return ui.New(specData), nil
}

func loadSpec() (map[string]any, error) {
	if *localDir != "" {
		return loadFromDir(*localDir)
	}
	return loadFromURL(*apiURL)
}

func loadFromDir(dir string) (map[string]any, error) {
	data, err := readSpecFile(dir, "openapi.yaml")
	if err != nil {
		return nil, fmt.Errorf("reading openapi.yaml: %w", err)
	}

	mainSpec, err := parseSpec(data, "openapi.yaml")
	if err != nil {
		return nil, err
	}

	fileContents, err := loadAllFiles(dir, mainSpec)
	if err != nil {
		return nil, err
	}

	return mergeSpecs(mainSpec, fileContents), nil
}

func loadFromURL(baseURL string) (map[string]any, error) {
	if err := validateAPIURL(baseURL); err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}

	data, err := fetchContent(client, baseURL+"/api/OpenAPI/openapi.yaml")
	if err != nil {
		return nil, fmt.Errorf("fetching openapi.yaml: %w", err)
	}

	mainSpec, err := parseSpec(data, "openapi.yaml")
	if err != nil {
		return nil, err
	}

	fileContents := map[string]map[string]any{
		"openapi.yaml": mainSpec,
	}
	for _, ref := range extractFileRefs(mainSpec) {
		if !validSpecFile(ref) {
			log.Printf("warning: ignoring invalid spec reference %q", ref)
			continue
		}
		data, err := fetchContent(client, baseURL+"/api/OpenAPI/"+url.PathEscape(ref))
		if err != nil {
			log.Printf("warning: failed to fetch %s: %v", ref, err)
			continue
		}
		doc, err := parseSpec(data, ref)
		if err != nil {
			log.Printf("warning: failed to parse %s: %v", ref, err)
			continue
		}
		fileContents[ref] = doc
	}

	return mergeSpecs(mainSpec, fileContents), nil
}

func validateAPIURL(baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid API URL: %w", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("API URL must use HTTP or HTTPS")
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("API URL must include a hostname")
	}
	ip := net.ParseIP(hostname)
	isLoopback := strings.EqualFold(strings.TrimSuffix(hostname, "."), "localhost") ||
		(ip != nil && ip.IsLoopback())
	if scheme == "http" && !isLoopback {
		return fmt.Errorf("API URL must use HTTPS unless it targets localhost or a loopback IP")
	}
	return nil
}

func parseSpec(data []byte, name string) (map[string]any, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", name, err)
	}
	if doc == nil {
		return nil, fmt.Errorf("%s is empty", name)
	}
	normalized, ok := normalize(doc).(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s is not a mapping", name)
	}
	return normalized, nil
}

func readSpecFile(dir, name string) ([]byte, error) {
	file, err := os.OpenInRoot(dir, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readFileLimited(file)
}

func readFileLimited(file *os.File) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSpecSize {
		return nil, fmt.Errorf("%s exceeds %d bytes", filepath.Base(file.Name()), maxSpecSize)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSpecSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSpecSize {
		return nil, fmt.Errorf("%s exceeds %d bytes", filepath.Base(file.Name()), maxSpecSize)
	}
	return data, nil
}

func fetchContent(client *http.Client, target string) ([]byte, error) {
	response, err := client.Get(target)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", response.StatusCode, response.Status)
	}
	return readAllLimited(response.Body, maxSpecSize)
}

func loadAllFiles(dir string, mainSpec map[string]any) (map[string]map[string]any, error) {
	fileContents := map[string]map[string]any{
		"openapi.yaml": mainSpec,
	}

	for _, ref := range extractFileRefs(mainSpec) {
		if !validSpecFile(ref) {
			return nil, fmt.Errorf("invalid spec reference %q", ref)
		}
		data, err := readSpecFile(dir, ref)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", ref, err)
		}
		doc, err := parseSpec(data, ref)
		if err != nil {
			return nil, err
		}
		fileContents[ref] = doc
	}

	return fileContents, nil
}

func validSpecFile(ref string) bool {
	if ref == "" || ref == "." || ref == ".." {
		return false
	}
	if strings.ContainsAny(ref, `/\`) {
		return false
	}
	if !filepath.IsLocal(ref) {
		return false
	}
	return filepath.Base(ref) == ref
}

func extractFileRefs(doc map[string]any) []string {
	var files []string
	seen := make(map[string]bool)

	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return files
	}

	for _, item := range paths {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}

		ref, ok := itemMap["$ref"].(string)
		if !ok {
			continue
		}

		if !strings.HasPrefix(ref, "./") {
			continue
		}

		hashIdx := strings.Index(ref, "#")
		if hashIdx == -1 {
			continue
		}

		file := ref[2:hashIdx]
		if !seen[file] {
			seen[file] = true
			files = append(files, file)
		}
	}

	return files
}

// --- Spec merging ---

func mergeSpecs(mainSpec map[string]any, fileContents map[string]map[string]any) map[string]any {
	if mainSpec == nil {
		return map[string]any{}
	}
	mergeComponents(mainSpec, fileContents)
	resolvePathRefs(mainSpec, fileContents)
	fixCrossFileRefs(mainSpec)

	if servers, ok := mainSpec["servers"].([]any); ok && len(servers) == 0 {
		mainSpec["servers"] = []any{map[string]any{"url": "/"}}
	}

	return mainSpec
}

func mergeComponents(mainSpec map[string]any, fileContents map[string]map[string]any) {
	mainComponents, ok := mainSpec["components"].(map[string]any)
	if !ok {
		mainComponents = make(map[string]any)
		mainSpec["components"] = mainComponents
	}

	for filename, doc := range fileContents {
		if filename == "openapi.yaml" || doc == nil {
			continue
		}

		docComponents, ok := doc["components"].(map[string]any)
		if !ok {
			continue
		}

		for compType, compItems := range docComponents {
			compItemsMap, ok := compItems.(map[string]any)
			if !ok {
				continue
			}

			mainCompType, ok := mainComponents[compType].(map[string]any)
			if !ok {
				mainCompType = make(map[string]any)
				mainComponents[compType] = mainCompType
			}

			for name, value := range compItemsMap {
				if _, exists := mainCompType[name]; !exists {
					mainCompType[name] = deepCopy(value)
				}
			}
		}
	}
}

func resolvePathRefs(mainSpec map[string]any, fileContents map[string]map[string]any) {
	paths, ok := mainSpec["paths"].(map[string]any)
	if !ok {
		return
	}

	for path, item := range paths {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}

		ref, ok := itemMap["$ref"].(string)
		if !ok {
			continue
		}

		hashIdx := strings.Index(ref, "#")
		if hashIdx == -1 {
			continue
		}

		file := strings.TrimPrefix(ref[:hashIdx], "./")
		jsonPtr := strings.TrimPrefix(ref[hashIdx+1:], "#")

		doc, ok := fileContents[file]
		if !ok || doc == nil {
			continue
		}

		resolved, err := jsonPointerGet(doc, jsonPtr)
		if err != nil {
			continue
		}

		paths[path] = deepCopy(resolved)
	}
}

func fixCrossFileRefs(node any) {
	switch v := node.(type) {
	case map[string]any:
		for key, val := range v {
			if key == "$ref" {
				if refStr, ok := val.(string); ok {
					v[key] = fixRef(refStr)
				}
			} else {
				fixCrossFileRefs(val)
			}
		}
	case map[any]any:
		for key, val := range v {
			if key == "$ref" {
				if refStr, ok := val.(string); ok {
					v[key] = fixRef(refStr)
				}
			} else {
				fixCrossFileRefs(val)
			}
		}
	case []any:
		for _, item := range v {
			fixCrossFileRefs(item)
		}
	}
}

func fixRef(ref string) string {
	if strings.HasPrefix(ref, "./openapi.yaml#") {
		return strings.TrimPrefix(ref, "./openapi.yaml")
	}
	if strings.HasPrefix(ref, "./") {
		hashIdx := strings.Index(ref, "#")
		if hashIdx != -1 {
			return ref[hashIdx:]
		}
	}
	return ref
}

// --- JSON Pointer ---

func jsonPointerGet(doc any, ptr string) (any, error) {
	if !strings.HasPrefix(ptr, "/") {
		return nil, fmt.Errorf("invalid JSON pointer: must start with /, got %q", ptr)
	}

	ptr = ptr[1:]
	current := doc
	if ptr == "" {
		return doc, nil
	}

	for _, token := range strings.Split(ptr, "/") {
		token = decodeJSONPointerToken(token)

		switch v := current.(type) {
		case map[string]any:
			next, ok := v[token]
			if !ok {
				return nil, fmt.Errorf("key %q not found", token)
			}
			current = next
		case map[any]any:
			next, ok := v[token]
			if !ok {
				return nil, fmt.Errorf("key %q not found", token)
			}
			current = next
		case []any:
			idx, err := strconv.Atoi(token)
			if err != nil {
				return nil, fmt.Errorf("invalid array index %q: %w", token, err)
			}
			if idx < 0 || idx >= len(v) {
				return nil, fmt.Errorf("array index %d out of range (len %d)", idx, len(v))
			}
			current = v[idx]
		default:
			return nil, fmt.Errorf("cannot navigate into %T", current)
		}
	}

	return current, nil
}

func decodeJSONPointerToken(token string) string {
	token = strings.ReplaceAll(token, "~1", "/")
	token = strings.ReplaceAll(token, "~0", "~")
	return token
}

// --- Utilities ---

func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalize(val)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprintf("%v", k)] = normalize(val)
		}
		return m
	case []any:
		for i, val := range t {
			t[i] = normalize(val)
		}
		return t
	default:
		return v
	}
}

func deepCopy(v any) any {
	switch v := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(v))
		for k, val := range v {
			m[k] = deepCopy(val)
		}
		return m
	case map[any]any:
		m := make(map[string]any, len(v))
		for k, val := range v {
			m[fmt.Sprintf("%v", k)] = deepCopy(val)
		}
		return m
	case []any:
		s := make([]any, len(v))
		for i, val := range v {
			s[i] = deepCopy(val)
		}
		return s
	default:
		return v
	}
}

func countOperations(paths map[string]any) int {
	count := 0
	for _, item := range paths {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for key := range itemMap {
			if isHTTPMethod(key) {
				count++
			}
		}
	}
	return count
}

func countAllMethods() map[string]int {
	specMu.RLock()
	defer specMu.RUnlock()
	if specData == nil {
		return nil
	}
	counts := make(map[string]int)
	paths, ok := specData["paths"].(map[string]any)
	if !ok {
		return counts
	}
	for _, item := range paths {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for key := range itemMap {
			if isHTTPMethod(key) {
				counts[key]++
			}
		}
	}
	return counts
}

func isHTTPMethod(s string) bool {
	switch strings.ToLower(s) {
	case "get", "post", "put", "delete", "patch", "head", "options", "trace":
		return true
	}
	return false
}
