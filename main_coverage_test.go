package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pharrisee/seven-api/ui"
)

func useUnavailableSpec(t *testing.T) {
	t.Helper()
	specMu.Lock()
	oldData, oldAt, oldErr := specData, specLoadedAt, specErr
	specData, specLoadedAt, specErr = nil, time.Time{}, errSpecUnavailable
	specMu.Unlock()
	t.Cleanup(func() {
		specMu.Lock()
		specData, specLoadedAt, specErr = oldData, oldAt, oldErr
		specMu.Unlock()
	})
}

func TestPageAndSidebarHandlers(t *testing.T) {
	useTestSpec(t, "http://127.0.0.1:1")

	for _, path := range []string{"/", "/op?method=GET&path=/api/player"} {
		recorder := httptest.NewRecorder()
		handleIndex(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s: status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	handleIndex(recorder, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("unknown route status = %d, want 404", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	handleIndex(recorder, httptest.NewRequest(http.MethodPut, "/", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("unsupported method status = %d, want 405", recorder.Code)
	}

	sidebar := httptest.NewRequest(http.MethodGet, "/ui/sidebar", nil)
	sidebar.Header.Set("HX-Current-URL", "http://viewer.test/op?method=GET&path=%2Fapi%2Fplayer&q=player")
	recorder = httptest.NewRecorder()
	handleUISidebar(recorder, sidebar)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Players") && !strings.Contains(recorder.Body.String(), "/api/player") {
		t.Fatalf("sidebar response = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handleUISidebar(recorder, httptest.NewRequest(http.MethodPost, "/ui/sidebar", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("sidebar unsupported method status = %d", recorder.Code)
	}

	useUnavailableSpec(t)
	for _, handler := range []http.HandlerFunc{handleIndex, handleUISidebar, handleUIOp} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		recorder = httptest.NewRecorder()
		handler(recorder, request)
		if recorder.Code != http.StatusServiceUnavailable && !strings.Contains(recorder.Body.String(), "unavailable") {
			t.Errorf("unavailable handler response = %d %s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestJSONAPIHandlers(t *testing.T) {
	useTestSpec(t, "http://127.0.0.1:1")
	dir := t.TempDir()
	oldDir := *localDir
	*localDir = dir
	t.Cleanup(func() { *localDir = oldDir })
	if err := os.WriteFile(filepath.Join(dir, "openapi.yaml"), []byte("openapi: 3.0.0\npaths: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	handleSpec(recorder, httptest.NewRequest(http.MethodGet, "/api/spec", nil))
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("spec response = %d %s", recorder.Code, recorder.Body.String())
	}
	var document map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil || document["openapi"] != "3.0.0" {
		t.Fatalf("invalid spec response: %v %v", document, err)
	}
	recorder = httptest.NewRecorder()
	handleSpec(recorder, httptest.NewRequest(http.MethodPost, "/api/spec", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("spec unsupported method status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handleConfig(recorder, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "127.0.0.1:1") {
		t.Errorf("config response = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handleConfig(recorder, httptest.NewRequest(http.MethodPost, "/api/config", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("config unsupported method status = %d", recorder.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/reload", nil)
	recorder = httptest.NewRecorder()
	handleReload(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("reload status = %d: %s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/reload", nil)
	recorder = httptest.NewRecorder()
	handleReload(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("reload unsupported method status = %d", recorder.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/reload", nil)
	request.Header.Set("Origin", "http://attacker.test")
	recorder = httptest.NewRecorder()
	handleReload(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Errorf("cross-origin reload status = %d", recorder.Code)
	}

	useUnavailableSpec(t)
	recorder = httptest.NewRecorder()
	handleSpec(recorder, httptest.NewRequest(http.MethodGet, "/api/spec", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("unavailable spec status = %d", recorder.Code)
	}
}

func TestReloadHandlers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openapi.yaml")
	oldDir := *localDir
	*localDir = dir
	t.Cleanup(func() { *localDir = oldDir })
	if err := os.WriteFile(path, []byte("openapi: 3.0.0\ninfo: {title: reload, version: \"1\"}\npaths: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/ui/reload", nil)
	request.Header.Set("HX-Request", "true")
	recorder := httptest.NewRecorder()
	handleUIReload(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("HX-Refresh") != "true" {
		t.Fatalf("HX reload = %d headers=%v", recorder.Code, recorder.Header())
	}

	recorder = httptest.NewRecorder()
	handleUIReload(recorder, httptest.NewRequest(http.MethodPost, "/ui/reload", nil))
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/" {
		t.Fatalf("browser reload = %d location=%q", recorder.Code, recorder.Header().Get("Location"))
	}

	recorder = httptest.NewRecorder()
	handleUIReload(recorder, httptest.NewRequest(http.MethodGet, "/ui/reload", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("reload unsupported method status = %d", recorder.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/ui/reload", nil)
	request.Header.Set("Origin", "http://attacker.test")
	recorder = httptest.NewRecorder()
	handleUIReload(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Errorf("cross-origin UI reload status = %d", recorder.Code)
	}

	if err := os.WriteFile(path, []byte("paths: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handleUIReload(recorder, httptest.NewRequest(http.MethodPost, "/ui/reload", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Reload failed") {
		t.Errorf("failed reload = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLoadFromURLAndFetchContent(t *testing.T) {
	const root = "openapi: 3.0.0\ninfo: {title: remote, version: \"1\"}\npaths:\n  /remote:\n    $ref: ./Paths.openapi.yaml#/paths/~1remote\n"
	const paths = "paths:\n  /remote:\n    get:\n      responses:\n        \"200\": {description: ok}\ncomponents:\n  schemas:\n    Result: {type: string}\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/OpenAPI/openapi.yaml":
			_, _ = io.WriteString(w, root)
		case "/api/OpenAPI/Paths.openapi.yaml":
			_, _ = io.WriteString(w, paths)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	spec, err := loadFromURL(server.URL)
	if err != nil {
		t.Fatalf("loadFromURL: %v", err)
	}
	if _, ok := ui.New(spec).FindOperation("GET", "/remote"); !ok {
		t.Fatalf("remote path ref was not resolved: %#v", spec["paths"])
	}
	components := spec["components"].(map[string]any)
	if _, ok := components["schemas"].(map[string]any)["Result"]; !ok {
		t.Fatalf("referenced components were not merged: %#v", components)
	}
	if _, err := loadFromURL("ftp://localhost"); err == nil {
		t.Fatal("invalid URL unexpectedly loaded")
	}

	client := &http.Client{}
	server404 := httptest.NewServer(http.NotFoundHandler())
	defer server404.Close()
	if _, err := fetchContent(client, server404.URL); err == nil {
		t.Fatal("fetchContent succeeded for a not-found response")
	}
	if _, err := fetchContent(client, "http://127.0.0.1:1"); err == nil {
		t.Fatal("fetchContent suppressed a connection error")
	}

	badRefServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/OpenAPI/openapi.yaml" {
			_, _ = io.WriteString(w, "openapi: 3.0.0\npaths:\n  /bad:\n    $ref: ./Bad.yaml#/paths/~1bad\n")
			return
		}
		_, _ = io.WriteString(w, "paths: [")
	}))
	defer badRefServer.Close()
	if _, err := loadFromURL(badRefServer.URL); err != nil {
		t.Fatalf("loadFromURL should skip an invalid referenced file: %v", err)
	}
}

func TestSpecFileLoadingAndParsing(t *testing.T) {
	if _, err := parseSpec([]byte("["), "broken.yaml"); err == nil {
		t.Fatal("parseSpec accepted malformed YAML")
	}
	if _, err := parseSpec([]byte("null"), "empty.yaml"); err == nil {
		t.Fatal("parseSpec accepted an empty document")
	}
	if _, err := parseSpec([]byte("- item"), "list.yaml"); err == nil {
		t.Fatal("parseSpec accepted a non-mapping document")
	}

	dir := t.TempDir()
	main := []byte("openapi: 3.0.0\npaths:\n  /x:\n    $ref: ./Paths.yaml#/paths/~1x\n")
	if err := os.WriteFile(filepath.Join(dir, "openapi.yaml"), main, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Paths.yaml"), []byte("paths:\n  /x:\n    get: {responses: {\"200\": {description: ok}}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := loadFromDir(dir)
	if err != nil {
		t.Fatalf("loadFromDir: %v", err)
	}
	if _, ok := ui.New(spec).FindOperation("GET", "/x"); !ok {
		t.Fatal("local path reference was not resolved")
	}
	if _, err := loadFromDir(t.TempDir()); err == nil {
		t.Fatal("loadFromDir succeeded without openapi.yaml")
	}

	mainSpec := map[string]any{"paths": map[string]any{
		"/valid":     map[string]any{"$ref": "./A.yaml#/paths/~1valid"},
		"/dup":       map[string]any{"$ref": "./A.yaml#/paths/~1valid"},
		"/no-hash":   map[string]any{"$ref": "./A.yaml"},
		"/external":  map[string]any{"$ref": "https://example.test/openapi.yaml#/paths/~1x"},
		"/bad":       map[string]any{"$ref": "./../escape.yaml#/paths/~1x"},
		"/bad-shape": "not a path",
	}}
	refs := extractFileRefs(mainSpec)
	gotRefs := map[string]bool{}
	for _, ref := range refs {
		gotRefs[ref] = true
	}
	if len(refs) != 2 || !gotRefs["A.yaml"] || !gotRefs["../escape.yaml"] {
		t.Errorf("extractFileRefs() = %v", refs)
	}
	if _, err := loadAllFiles(dir, map[string]any{"paths": map[string]any{
		"/escape": map[string]any{"$ref": "./../escape.yaml#/paths"},
	}}); err == nil {
		t.Fatal("loadAllFiles accepted an unsafe reference")
	}
	if _, err := loadAllFiles(dir, map[string]any{"paths": map[string]any{
		"/missing": map[string]any{"$ref": "./Missing.yaml#/paths"},
	}}); err == nil {
		t.Fatal("loadAllFiles accepted a missing file")
	}
}

func TestSpecMergingAndPointers(t *testing.T) {
	if got := mergeSpecs(nil, nil); len(got) != 0 {
		t.Fatalf("mergeSpecs(nil) = %v", got)
	}
	main := map[string]any{
		"servers":    []any{},
		"components": map[string]any{"schemas": map[string]any{"Existing": map[string]any{"type": "string"}}},
		"paths": map[string]any{
			"/valid": map[string]any{"$ref": "./Paths.yaml#/paths/~1valid"},
			"/bad":   map[string]any{"$ref": "./missing.yaml#/paths/~1bad"},
		},
	}
	files := map[string]map[string]any{
		"openapi.yaml": main,
		"Paths.yaml": {
			"paths": map[string]any{"/valid": map[string]any{"get": map[string]any{"responses": map[string]any{}}}},
			"components": map[string]any{"schemas": map[string]any{
				"Existing": map[string]any{"type": "number"},
				"Added":    map[string]any{"type": "boolean"},
			}},
		},
		"nil.yaml": nil,
		"bad.yaml": {"components": "invalid"},
	}
	got := mergeSpecs(main, files)
	if got["servers"].([]any)[0].(map[string]any)["url"] != "/" {
		t.Fatalf("empty servers were not defaulted: %v", got["servers"])
	}
	if _, ok := ui.New(got).FindOperation("GET", "/valid"); !ok {
		t.Fatal("path reference was not merged")
	}
	schemas := got["components"].(map[string]any)["schemas"].(map[string]any)
	if schemas["Existing"].(map[string]any)["type"] != "string" || schemas["Added"].(map[string]any)["type"] != "boolean" {
		t.Fatalf("components merged incorrectly: %v", schemas)
	}

	for ref, want := range map[string]string{
		"./openapi.yaml#/components/schemas/A": "#/components/schemas/A",
		"./Other.yaml#/components/schemas/A":   "#/components/schemas/A",
		"./Other.yaml":                         "./Other.yaml",
		"external.yaml#/A":                     "external.yaml#/A",
	} {
		if got := fixRef(ref); got != want {
			t.Errorf("fixRef(%q) = %q, want %q", ref, got, want)
		}
	}
	refs := map[string]any{"nested": []any{
		map[string]any{"$ref": "./openapi.yaml#/components/schemas/A"},
		map[string]any{"$ref": "./Other.yaml#/components/schemas/B"},
	}}
	fixCrossFileRefs(refs)
	nested := refs["nested"].([]any)
	if nested[0].(map[string]any)["$ref"] != "#/components/schemas/A" ||
		nested[1].(map[string]any)["$ref"] != "#/components/schemas/B" {
		t.Fatalf("cross-file references were not rewritten: %v", refs)
	}

	doc := map[string]any{
		"a/b":  map[string]any{"~key": "value"},
		"list": []any{"zero", map[any]any{"name": "nested"}},
	}
	for ptr, want := range map[string]any{
		"/a~1b/~0key":  "value",
		"/list/0":      "zero",
		"/list/1/name": "nested",
		"":             doc,
	} {
		if ptr == "" {
			if got, err := jsonPointerGet(doc, ptr); err == nil || got != nil {
				t.Errorf("empty pointer = %v, %v; want error", got, err)
			}
			continue
		}
		if got, err := jsonPointerGet(doc, ptr); err != nil || got != want {
			t.Errorf("jsonPointerGet(%q) = %v, %v; want %v", ptr, got, err, want)
		}
	}
	for _, ptr := range []string{"x", "/missing", "/list/nope", "/list/-1", "/list/10", "/a~1b/value/inside"} {
		if _, err := jsonPointerGet(doc, ptr); err == nil {
			t.Errorf("jsonPointerGet(%q) succeeded, want error", ptr)
		}
	}
	if decodeJSONPointerToken("a~1b~0c") != "a/b~c" {
		t.Fatal("JSON pointer token escapes were not decoded")
	}
}

func TestSpecUtilityBranches(t *testing.T) {
	input := map[any]any{"nested": map[any]any{"value": []any{1, "x"}}}
	normalized := normalize(input).(map[string]any)
	if normalized["nested"].(map[string]any)["value"].([]any)[1] != "x" {
		t.Fatalf("normalize failed: %v", normalized)
	}
	copied := deepCopy(input).(map[string]any)
	copied["nested"].(map[string]any)["value"].([]any)[0] = 9
	if input["nested"].(map[any]any)["value"].([]any)[0] != 1 {
		t.Fatal("deepCopy retained nested references")
	}
	copiedSlice := deepCopy([]any{map[string]any{"x": true}}).([]any)
	copiedSlice[0].(map[string]any)["x"] = false
	if deepCopy("plain") != "plain" {
		t.Fatal("deepCopy changed scalar")
	}

	if got := countOperations(map[string]any{
		"/a": map[string]any{"get": nil, "parameters": map[string]any{}},
		"/b": "not a path item",
	}); got != 1 {
		t.Errorf("countOperations() = %d, want 1", got)
	}
	for _, method := range []string{"get", "POST", "put", "DELETE", "patch", "head", "options", "trace"} {
		if !isHTTPMethod(method) {
			t.Errorf("isHTTPMethod(%q) = false", method)
		}
	}
	if isHTTPMethod("connect") {
		t.Error("CONNECT should not be treated as an OpenAPI operation")
	}

	specMu.Lock()
	old := specData
	specData = map[string]any{"paths": map[string]any{
		"/a": map[string]any{"get": map[string]any{}, "post": map[string]any{}},
		"/b": "invalid",
	}}
	specMu.Unlock()
	if counts := countAllMethods(); counts["get"] != 1 || counts["post"] != 1 {
		t.Errorf("countAllMethods() = %v", counts)
	}
	specMu.Lock()
	specData = nil
	specMu.Unlock()
	if got := countAllMethods(); got != nil {
		t.Errorf("countAllMethods without spec = %v, want nil", got)
	}
	specMu.Lock()
	specData = old
	specMu.Unlock()

	var logOutput bytes.Buffer
	oldWriter, oldFlags := log.Writer(), log.Flags()
	log.SetOutput(&logOutput)
	log.SetFlags(0)
	logSpecInfo()
	request := httptest.NewRequest(http.MethodGet, "/x", nil)
	request.URL.RawQuery = "q=1\nforged"
	loggingHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})).ServeHTTP(httptest.NewRecorder(), request)
	log.SetOutput(oldWriter)
	log.SetFlags(oldFlags)
	if strings.Contains(logOutput.String(), "q=1\nforged") || !strings.Contains(logOutput.String(), "200 2B") {
		t.Errorf("unexpected request log: %q", logOutput.String())
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestLimitedReadsAndProxyValidation(t *testing.T) {
	if got, err := readAllLimited(strings.NewReader("abc"), 3); err != nil || string(got) != "abc" {
		t.Fatalf("readAllLimited = %q, %v", got, err)
	}
	if _, err := readAllLimited(strings.NewReader("abcd"), 3); err == nil {
		t.Fatal("readAllLimited accepted an oversized body")
	}
	if _, err := readAllLimited(failingReader{}, 3); err == nil {
		t.Fatal("readAllLimited suppressed the reader error")
	}

	for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
		if !supportedMethod(method) {
			t.Errorf("supportedMethod(%q) = false", method)
		}
	}
	if supportedMethod("TRACE") {
		t.Error("TRACE should not be supported")
	}
	for _, header := range []string{"X-Test", "content_type", "Accept"} {
		if !validHeaderName(header) {
			t.Errorf("validHeaderName(%q) = false", header)
		}
	}
	for _, header := range []string{"", "bad header", "x:y", "x\x00"} {
		if validHeaderName(header) {
			t.Errorf("validHeaderName(%q) = true", header)
		}
	}
	if !validHeaderValue("safe") || validHeaderValue("bad\r\nInjected: x") || validHeaderValue("bad\x00") {
		t.Error("validHeaderValue did not reject control characters")
	}
	if mergeCookie("", "a=1") != "a=1" || mergeCookie("a=1", "") != "a=1" || mergeCookie("a=1", "b=2") != "a=1; b=2" {
		t.Error("mergeCookie returned unexpected values")
	}
	if sessionCookie("opaque") != "sid=opaque" || sessionCookie("name=value") != "name=value" {
		t.Error("sessionCookie returned unexpected value")
	}

	oldURL := *apiURL
	t.Cleanup(func() { *apiURL = oldURL })
	*apiURL = "http://localhost:1"
	for _, input := range []struct {
		method, path, query string
	}{
		{"TRACE", "/x", ""},
		{"GET", "/x", "bad\nquery"},
	} {
		if _, err := doProxy(input.method, input.path, input.query, "", nil); err == nil {
			t.Errorf("doProxy(%+v) succeeded", input)
		}
	}
}

func TestStaticAndHealthHandlerBranches(t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, "/static/app.js", http.StatusMethodNotAllowed},
		{http.MethodGet, "/static/../main.go", http.StatusBadRequest},
		{http.MethodGet, "/static/missing.file", http.StatusNotFound},
		{http.MethodGet, "/static/htmx.min.js", http.StatusOK},
	} {
		recorder := httptest.NewRecorder()
		handleStatic(recorder, httptest.NewRequest(tt.method, tt.path, nil))
		if recorder.Code != tt.want {
			t.Errorf("%s %s status = %d, want %d", tt.method, tt.path, recorder.Code, tt.want)
		}
	}
	recorder := httptest.NewRecorder()
	handleStatic(recorder, httptest.NewRequest(http.MethodGet, "/static/htmx.min.js", nil))
	etag := recorder.Header().Get("ETag")
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/static/htmx.min.js", nil)
	request.Header.Set("If-None-Match", `"other", *`)
	handleStatic(recorder, request)
	if recorder.Code != http.StatusNotModified || etag == "" {
		t.Errorf("wildcard ETag response = %d", recorder.Code)
	}

	useUnavailableSpec(t)
	recorder = httptest.NewRecorder()
	handleHealthz(recorder, httptest.NewRequest(http.MethodHead, "/healthz", nil))
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("health body: %v", err)
	}
	if payload["specLoaded"] != false {
		t.Errorf("health specLoaded = %v", payload["specLoaded"])
	}
}
