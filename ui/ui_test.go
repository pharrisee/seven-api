package ui

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	g "maragu.dev/gomponents"
)

func render(t *testing.T, node g.Node) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := node.Render(&buffer); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buffer.String()
}

func TestPageEscapesSpecValues(t *testing.T) {
	spec := New(map[string]any{
		"info": map[string]any{
			"title":   `<script>alert(1)</script>`,
			"version": "1",
		},
		"paths": map[string]any{
			"/x": map[string]any{
				"get": map[string]any{
					"summary": `<img src=x onerror=alert(1)>`,
					"parameters": []any{
						map[string]any{
							"name":   "p",
							"in":     `<img src=x onerror="window.__xss=1">`,
							"schema": map[string]any{"type": "string"},
						},
					},
					"responses": map[string]any{
						"200": map[string]any{"description": "ok"},
					},
				},
			},
		},
	})

	html := render(t, Page(spec, "http://localhost:9898", "GET", "/x", "", State{}))
	for _, raw := range []string{`<script>alert(1)</script>`, `<img src=x onerror=alert(1)>`, `<img src=x onerror="window.__xss=1">`} {
		if strings.Contains(html, raw) {
			t.Fatalf("page contains unescaped spec value %q", raw)
		}
	}
	if !strings.Contains(html, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("title was not rendered as text")
	}
	if !strings.Contains(html, "&lt;img src=x onerror=") {
		t.Fatalf("parameter location was not rendered as text")
	}
}

func TestTryItResultEscapesBody(t *testing.T) {
	html := render(t, TryItResult(ProxyResult{
		Status:     200,
		StatusText: "200 OK",
		Body:       `<script>alert(1)</script>`,
	}))
	if strings.Contains(html, `<script>alert(1)</script>`) {
		t.Fatalf("result body contains unescaped HTML")
	}
	if !strings.Contains(html, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("result body was not escaped")
	}
}

func TestSidebarDefaults(t *testing.T) {
	spec := New(map[string]any{
		"paths": map[string]any{
			"/a": map[string]any{"get": map[string]any{"tags": []any{"Commands"}, "responses": map[string]any{"200": map[string]any{"description": "ok"}}}},
			"/b": map[string]any{"get": map[string]any{"tags": []any{"Log"}, "responses": map[string]any{"200": map[string]any{"description": "ok"}}}},
		},
	})

	html := render(t, SidebarInner(spec, "", "", "", State{}))
	if !strings.Contains(html, `data-state-name="Commands" data-persist="1" open`) {
		t.Fatalf("Commands should be open by default:\n%s", html)
	}
	if strings.Contains(html, `data-state-name="Log" data-persist="1" open`) {
		t.Fatalf("Log should be closed by default:\n%s", html)
	}

	html = render(t, SidebarInner(spec, "a", "", "", State{}))
	if !strings.Contains(html, `data-state-name="Commands" data-persist="0" open`) {
		t.Fatalf("filtered groups should be open:\n%s", html)
	}
	if strings.Contains(html, `data-state-name="Log"`) {
		t.Fatalf("filter should remove non-matching groups:\n%s", html)
	}
}

func TestStateCookieRoundTrip(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	recorder := httptest.NewRecorder()
	WriteState(recorder, request, State{
		Tags:     map[string]bool{"Log": true},
		Sections: map[string]bool{"responses": false},
	})

	next := httptest.NewRequest("GET", "/", nil)
	for _, cookie := range recorder.Result().Cookies() {
		next.AddCookie(cookie)
	}
	state := ReadState(next)
	if !state.TagOpen("Log", false) {
		t.Fatalf("Log tag state was not persisted")
	}
	if state.TagOpen("Commands", true) != true {
		t.Fatalf("unknown tag should use the default")
	}
	if state.SectionOpen("responses") {
		t.Fatalf("responses section should be closed")
	}
	if !state.SectionOpen("parameters") {
		t.Fatalf("unknown section should default to open")
	}
}

func TestSpecFilterAndAllowlist(t *testing.T) {
	spec := New(map[string]any{
		"paths": map[string]any{
			"/api/player": map[string]any{
				"get": map[string]any{
					"summary": "Players list",
					"tags":    []any{"WorldState"},
					"responses": map[string]any{
						"200": map[string]any{"description": "ok"},
					},
				},
			},
			"/api/player/{id}": map[string]any{
				"get": map[string]any{
					"summary": "Player get",
					"tags":    []any{"WorldState"},
					"responses": map[string]any{
						"200": map[string]any{"description": "ok"},
					},
				},
			},
		},
	})

	if operations := spec.FilterOperations("player"); len(operations) != 2 {
		t.Fatalf("filter returned %d operations, want 2", len(operations))
	}
	if operations := spec.FilterOperations("nothing"); len(operations) != 0 {
		t.Fatalf("filter returned %d operations, want 0", len(operations))
	}
	if !spec.Allows("GET", "/api/player/123") {
		t.Fatalf("template path should allow a concrete path")
	}
	if spec.Allows("POST", "/api/player/123") {
		t.Fatalf("template path should not allow an undeclared method")
	}
	if spec.Allows("GET", "/etc/passwd") {
		t.Fatalf("undeclared path should not be allowed")
	}
}
