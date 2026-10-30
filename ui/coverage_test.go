package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSchemaViewBranches(t *testing.T) {
	spec := New(map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"Node": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"child": map[string]any{"$ref": "#/components/schemas/Node"},
					},
				},
			},
		},
	})
	schema := map[string]any{
		"type":     []any{"string", "null", 42},
		"required": []any{"name", 3},
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "A name",
				"examples":    []any{"Ada"},
				"minLength":   1,
				"maxLength":   12,
				"pattern":     "^[A-Z]",
				"example":     "Ada",
			},
			"node": map[string]any{"$ref": "#/components/schemas/Node"},
		},
		"items":   map[string]any{"type": "integer"},
		"enum":    []any{"one", 2, true},
		"minimum": 1,
		"maximum": 9,
	}
	html := render(t, SchemaView(spec, schema))
	for _, want := range []string{
		"type-string", "type-null", "prop-required", "A name", "example:",
		"minLength: 1", "maxLength: 12", "^[A-Z]", "items:", "enum: one, 2, true",
		"min: 1", "max: 9", "Node",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("schema view missing %q:\n%s", want, html)
		}
	}

	for _, value := range []any{
		map[string]any{"oneOf": []any{map[string]any{"type": "string"}}},
		map[string]any{"anyOf": []any{map[string]any{"type": "integer"}}},
		map[string]any{"allOf": []any{map[string]any{"type": "boolean"}}},
		map[string]any{"enum": []any{"a"}},
		map[string]any{"properties": map[string]any{"x": map[string]any{"type": "string"}}},
		map[string]any{"items": map[string]any{"type": "string"}},
		map[string]any{"type": "unknown"},
		nil,
		"not a schema",
	} {
		_ = render(t, SchemaView(spec, value))
	}

	deep := map[string]any{"type": "string"}
	for range 12 {
		deep = map[string]any{"properties": map[string]any{"next": deep}}
	}
	if html := render(t, SchemaView(spec, deep)); !strings.Contains(html, "…") {
		t.Fatalf("deep schema did not render the depth limit: %s", html)
	}
}

func TestSchemaHelpers(t *testing.T) {
	for name, want := range map[string]string{
		"object": "type-object", "array": "type-array", "string": "type-string",
		"file": "type-string", "integer": "type-integer", "number": "type-number",
		"boolean": "type-boolean", "null": "type-null", "enum": "type-enum",
		"reference": "type-ref",
	} {
		if got := typeClass(name); got != want {
			t.Errorf("typeClass(%q) = %q, want %q", name, got, want)
		}
	}
	if got := schemaTypes(map[string]any{"type": []any{"", 4}}); len(got) != 0 {
		t.Errorf("schemaTypes with empty union = %v, want no types", got)
	}
	if got := stringSet([]any{"a", "", 4, "a"}); len(got) != 1 || !got["a"] {
		t.Errorf("stringSet = %v, want only a", got)
	}
	if got := copyVisited(map[string]bool{"a": true}); !got["a"] {
		t.Errorf("copyVisited did not copy existing entries: %v", got)
	}
}

func TestSpecMetadataResolveAndLabels(t *testing.T) {
	raw := map[string]any{
		"info":    map[string]any{"title": "Title", "version": "2"},
		"servers": []any{map[string]any{"url": "https://api.example"}, map[string]any{"url": "ignored"}},
		"components": map[string]any{"schemas": map[string]any{
			"Pet/~type": map[string]any{"type": "string"},
		}},
		"list": []any{"first", "second"},
		"paths": map[string]any{"/x": map[string]any{"get": map[string]any{
			"tags": []any{"Commands"},
		}}},
	}
	spec := New(raw)
	if asMap(spec.Raw()["info"])["title"] != "Title" || spec.Title() != "Title" || spec.Version() != "2" {
		t.Fatal("spec metadata did not preserve the raw document")
	}
	if spec.ServerURL() != "https://api.example" || New(nil).ServerURL() != "" {
		t.Fatal("unexpected server URL")
	}
	if New(nil).Title() != "7D2D API" {
		t.Fatal("missing title did not use the default")
	}
	if got := spec.Resolve("#/components/schemas/Pet~1~0type"); got == nil {
		t.Fatal("escaped JSON pointer did not resolve")
	}
	if got := spec.Resolve("#/list/1"); got != "second" {
		t.Fatalf("array pointer = %v, want second", got)
	}
	for _, ref := range []string{"components/schemas/Pet", "#/missing", "#/list/nope", "#/list/5", "#/info/title/value"} {
		if got := spec.Resolve(ref); got != nil {
			t.Errorf("Resolve(%q) = %v, want nil", ref, got)
		}
	}
	if !spec.Operations()[0].HasTag("commands") || spec.Operations()[0].HasTag("missing") {
		t.Fatal("HasTag returned an unexpected result")
	}

	tests := []struct {
		value any
		want  string
	}{
		{nil, "—"},
		{map[string]any{"$ref": "#/components/schemas/Pet/~1~0type"}, "~1~0type"},
		{map[string]any{"type": "array"}, "array"},
		{map[string]any{"type": []any{"string", "", "null"}}, "string | null"},
		{map[string]any{"type": []any{4}}, "—"},
		{map[string]any{"oneOf": []any{}}, "composite"},
		{map[string]any{"enum": []any{"a"}}, "enum"},
		{map[string]any{}, "—"},
	}
	for _, tt := range tests {
		if got := spec.SchemaTypeLabel(tt.value); got != tt.want {
			t.Errorf("SchemaTypeLabel(%v) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestSpecExamples(t *testing.T) {
	spec := New(map[string]any{
		"components": map[string]any{"schemas": map[string]any{
			"Name": map[string]any{"type": "string", "default": "Ada"},
			"Loop": map[string]any{"$ref": "#/components/schemas/Loop"},
		}},
	})
	tests := []struct {
		name   string
		schema any
		want   any
	}{
		{"nil schema", nil, nil},
		{"example", map[string]any{"example": "example", "default": "default"}, "example"},
		{"default", map[string]any{"default": "default"}, "default"},
		{"const", map[string]any{"const": "constant"}, "constant"},
		{"examples", map[string]any{"examples": []any{"first", "second"}}, "first"},
		{"enum", map[string]any{"enum": []any{"first", "second"}}, "first"},
		{"oneOf", map[string]any{"oneOf": []any{map[string]any{"type": "boolean"}}}, true},
		{"anyOf", map[string]any{"anyOf": []any{map[string]any{"type": "integer"}}}, float64(0)},
		{"allOf", map[string]any{"allOf": []any{
			map[string]any{"example": map[string]any{"a": 1}},
			map[string]any{"example": map[string]any{"b": 2}},
		}}, map[string]any{"a": 1, "b": 2}},
		{"object", map[string]any{"type": "object", "properties": map[string]any{
			"name": map[string]any{"$ref": "#/components/schemas/Name"},
		}}, map[string]any{"name": "Ada"}},
		{"array", map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, []any{"string"}},
		{"empty array", map[string]any{"type": "array"}, []any{}},
		{"number minimum", map[string]any{"type": "number", "minimum": 2.5}, 2.5},
		{"integer zero", map[string]any{"type": "integer"}, float64(0)},
		{"boolean", map[string]any{"type": "boolean"}, true},
		{"date time", map[string]any{"type": "string", "format": "date-time"}, "2026-01-01T00:00:00Z"},
		{"uuid", map[string]any{"type": "string", "format": "uuid"}, "00000000-0000-0000-0000-000000000000"},
		{"pattern string", map[string]any{"type": "string", "pattern": "x"}, ""},
		{"string", map[string]any{"type": "string"}, "string"},
		{"union type", map[string]any{"type": []any{"null", "string"}}, "string"},
		{"unknown type", map[string]any{"type": "objectish"}, nil},
		{"reference", map[string]any{"$ref": "#/components/schemas/Name"}, "Ada"},
		{"recursive reference", map[string]any{"$ref": "#/components/schemas/Loop"}, map[string]any{}},
		{"missing reference", map[string]any{"$ref": "#/missing"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := spec.ExampleFromSchema(tt.schema); !equalJSONValue(got, tt.want) {
				t.Errorf("ExampleFromSchema() = %#v, want %#v", got, tt.want)
			}
		})
	}

	deep := any(map[string]any{"type": "string"})
	for range 10 {
		deep = map[string]any{"oneOf": []any{deep}}
	}
	if got := spec.ExampleFromSchema(deep); got != nil {
		t.Errorf("deep example = %v, want nil", got)
	}
}

func equalJSONValue(got, want any) bool {
	switch a := got.(type) {
	case map[string]any:
		b, ok := want.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for key, value := range a {
			if !equalJSONValue(value, b[key]) {
				return false
			}
		}
		return true
	case []any:
		b, ok := want.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !equalJSONValue(a[i], b[i]) {
				return false
			}
		}
		return true
	default:
		return got == want
	}
}

func TestAuthHeadersAndSections(t *testing.T) {
	spec := New(map[string]any{
		"components": map[string]any{"securitySchemes": map[string]any{
			"api-secret": map[string]any{"type": "apiKey", "in": "header", "name": "X-Secret"},
			"api-name":   map[string]any{"type": "apiKey", "in": "header", "name": "X-Name"},
			"bad-type":   map[string]any{"type": "http", "in": "header", "name": "ignored"},
			"bad-place":  map[string]any{"type": "apiKey", "in": "query", "name": "ignored"},
			"no-name":    map[string]any{"type": "apiKey", "in": "header"},
		}},
		"paths": map[string]any{"/secure": map[string]any{"get": map[string]any{
			"security":  []any{map[string]any{"api-secret": []any{}, "unknown": []any{}}},
			"responses": map[string]any{"200": map[string]any{"description": "ok"}},
			"requestBody": map[string]any{"required": true, "content": map[string]any{
				"application/json": map[string]any{"example": map[string]any{"ok": true}},
			}},
		}}},
	})
	name, secret := spec.AuthHeaders()
	if name != "X-Name" || secret != "X-Secret" {
		t.Fatalf("AuthHeaders() = %q, %q", name, secret)
	}
	op, ok := spec.FindOperation("get", "/secure")
	if !ok {
		t.Fatal("missing secure operation")
	}
	html := render(t, ContentView(spec, op.Method, op.Path, State{}, nil))
	for _, want := range []string{"Request Body", "required", "application/json", "Security", "api-secret", "unknown"} {
		if !strings.Contains(html, want) {
			t.Errorf("operation view missing %q:\n%s", want, html)
		}
	}

	for _, node := range []struct {
		name string
		html string
	}{
		{"notice", render(t, Notice("message"))},
		{"sidebar notice", render(t, SidebarNotice("message"))},
		{"error page", render(t, ErrorPage("title", "message"))},
	} {
		if !strings.Contains(node.html, "message") {
			t.Errorf("%s did not render its message", node.name)
		}
	}
}

func TestContentAndResultBranches(t *testing.T) {
	spec := New(map[string]any{"paths": map[string]any{
		"/empty": map[string]any{"post": map[string]any{
			"requestBody": map[string]any{"content": map[string]any{}},
			"responses": map[string]any{
				"default": map[string]any{"description": "fallback"},
				"204":     map[string]any{"description": "empty"},
				"302":     map[string]any{"description": "redirect"},
				"404":     map[string]any{"description": "missing"},
				"503":     map[string]any{"description": "failure"},
				"200": map[string]any{
					"description": "ok",
					"content": map[string]any{"application/json": map[string]any{
						"schema":  map[string]any{"type": "object"},
						"example": map[string]any{"ok": true},
					}},
					"headers": map[string]any{"X-Rate-Limit": map[string]any{
						"description": "requests left",
						"schema":      map[string]any{"type": "integer"},
					}},
				},
			},
		}},
	}})
	html := render(t, ContentView(spec, "POST", "/empty", State{}, nil))
	for _, want := range []string{"no content", "response-2xx", "response-3xx", "response-4xx", "response-5xx", "requests left", "fallback"} {
		if !strings.Contains(html, want) {
			t.Errorf("content view missing %q:\n%s", want, html)
		}
	}
	if got := render(t, ContentView(spec, "", "", State{}, nil)); !strings.Contains(got, "Select an endpoint") {
		t.Errorf("empty selection rendered unexpected content: %s", got)
	}
	if got := render(t, ContentView(spec, "GET", "/missing", State{}, nil)); !strings.Contains(got, "No operation found") {
		t.Errorf("missing operation rendered unexpected content: %s", got)
	}

	for status, want := range map[int]string{
		199: "server-error", 200: "ok", 302: "redirect", 404: "client-error", 500: "server-error",
	} {
		if got := tryItStatusClass(status); got != want {
			t.Errorf("tryItStatusClass(%d) = %q, want %q", status, got, want)
		}
	}
	for code, want := range map[string]string{
		"200": "response-2xx", "302": "response-3xx", "404": "response-4xx",
		"503": "response-5xx", "100": "", "abc": "",
	} {
		if got := responseClass(code); got != want {
			t.Errorf("responseClass(%q) = %q, want %q", code, got, want)
		}
	}

	for _, result := range []ProxyResult{
		{Error: "failed"},
		{Status: http.StatusNoContent, Body: ""},
		{Status: http.StatusOK, Body: "not JSON"},
		{Status: http.StatusOK, Body: `{"broken"`, Headers: map[string][]string{"content-type": {"application/json"}}},
		{Status: http.StatusOK, Body: "AA==", Binary: true},
	} {
		_ = render(t, TryItResult(result, ""))
	}
	_ = render(t, TryItResult(ProxyResult{
		Status: 200, Body: `{"ok":true}`, Headers: map[string][]string{"Content-Type": {"application/json"}, "X-Test": {"a", "b"}},
	}, "curl"))
	if got := firstHeader(map[string][]string{"content-type": {"application/json"}}, "Content-Type"); got != "application/json" {
		t.Errorf("firstHeader = %q", got)
	}
	if got := firstHeader(map[string][]string{"other": {"value"}, "empty": {}}, "Content-Type"); got != "" {
		t.Errorf("missing firstHeader = %q, want empty", got)
	}
}

func TestStateCookieInvalidAndSetters(t *testing.T) {
	for _, value := range []string{"%%%", "e30", "eyJ0YWdzIjoibm90LWEtbWFwIn0"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.AddCookie(&http.Cookie{Name: StateCookie, Value: value})
		state := ReadState(request)
		if len(state.Tags) != 0 || len(state.Sections) != 0 {
			t.Errorf("invalid cookie %q produced state: %+v", value, state)
		}
	}

	var state State
	state.SetTag("tag", true)
	state.SetSection("section", false)
	if !state.TagOpen("tag", false) || state.SectionOpen("section") {
		t.Fatalf("setters did not update state: %+v", state)
	}

	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	recorder := httptest.NewRecorder()
	WriteState(recorder, request, state)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected TLS state cookie: %+v", cookies)
	}
}

func TestSpecMoreBranches(t *testing.T) {
	spec := New(map[string]any{
		"paths": map[string]any{
			"/x": map[string]any{
				"get": map[string]any{
					"parameters": []any{
						map[string]any{"name": "shared", "in": "query", "required": true, "schema": map[string]any{"type": "string"}},
						map[string]any{"$ref": "#/components/parameters/Id"},
						"invalid",
					},
				},
				"parameters": []any{map[string]any{"name": "shared-path", "in": "path"}},
			},
		},
		"components": map[string]any{
			"parameters": map[string]any{"Id": map[string]any{"name": "id", "in": "path"}},
			"schemas": map[string]any{
				"A": map[string]any{"$ref": "#/components/schemas/B"},
				"B": map[string]any{"type": "string"},
			},
		},
	})
	op, ok := spec.FindOperation(" GET ", "/x")
	if !ok {
		t.Fatal("FindOperation did not normalize method")
	}
	if got := spec.Params(op); len(got) != 3 || got[0].Name != "shared-path" || got[1].Name != "shared" || got[2].Name != "id" {
		t.Errorf("Params() = %+v", got)
	}
	if got := spec.derefMap(map[string]any{"$ref": "#/components/schemas/A"}); asString(got["type"]) != "string" {
		t.Errorf("derefMap did not follow reference chain: %v", got)
	}
	if got := spec.derefMap(map[string]any{"$ref": "#/components/schemas/missing"}); got != nil {
		t.Errorf("derefMap unresolved reference = %v, want nil", got)
	}
}
