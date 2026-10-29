package ui

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// Spec is a read-only view over the merged OpenAPI document.
type Spec struct {
	raw map[string]any
}

func New(raw map[string]any) *Spec { return &Spec{raw: raw} }

func (s *Spec) Raw() map[string]any { return s.raw }

func (s *Spec) Title() string {
	info := asMap(s.raw["info"])
	if t := asString(info["title"]); t != "" {
		return t
	}
	return "7D2D API"
}

func (s *Spec) Version() string {
	return asString(asMap(s.raw["info"])["version"])
}

func (s *Spec) ServerURL() string {
	servers := asSlice(s.raw["servers"])
	if len(servers) == 0 {
		return ""
	}
	return asString(asMap(servers[0])["url"])
}

// --- Operations ---

var httpMethods = []string{"get", "post", "put", "delete", "patch", "head", "options", "trace"}

type Operation struct {
	Method string
	Path   string
	Op     map[string]any
	Tags   []string
}

func (o Operation) Summary() string     { return asString(o.Op["summary"]) }
func (o Operation) Description() string { return asString(o.Op["description"]) }
func (o Operation) OperationID() string { return asString(o.Op["operationId"]) }
func (o Operation) Deprecated() bool    { return asBool(o.Op["deprecated"]) }

func (o Operation) HasTag(tag string) bool {
	for _, t := range o.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

func (s *Spec) Operations() []Operation {
	paths := asMap(s.raw["paths"])
	var out []Operation
	for _, path := range sortedKeys(paths) {
		item := asMap(paths[path])
		if item == nil {
			continue
		}
		for _, method := range httpMethods {
			op := asMap(item[method])
			if op == nil {
				continue
			}
			var tags []string
			for _, t := range asSlice(op["tags"]) {
				if name := asString(t); name != "" {
					tags = append(tags, name)
				}
			}
			if len(tags) == 0 {
				tags = []string{"Untagged"}
			}
			out = append(out, Operation{Method: method, Path: path, Op: op, Tags: tags})
		}
	}
	return out
}

func (s *Spec) FindOperation(method, path string) (Operation, bool) {
	method = strings.ToLower(strings.TrimSpace(method))
	for _, op := range s.Operations() {
		if op.Method == method && op.Path == path {
			return op, true
		}
	}
	return Operation{}, false
}

// FilterOperations returns the operations matching a free-text filter over
// path, summary, operationId and tags.
func (s *Spec) FilterOperations(query string) []Operation {
	ops := s.Operations()
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return ops
	}
	out := make([]Operation, 0, len(ops))
	for _, op := range ops {
		haystack := strings.ToLower(op.Path + " " + op.Summary() + " " + op.OperationID() + " " + strings.Join(op.Tags, " "))
		if strings.Contains(haystack, query) {
			out = append(out, op)
		}
	}
	return out
}

// Allows reports whether the spec declares method at path. Path templates such
// as /api/player/{id} match concrete paths too.
func (s *Spec) Allows(method, path string) bool {
	method = strings.ToLower(strings.TrimSpace(method))
	for _, op := range s.Operations() {
		if op.Method != method {
			continue
		}
		if op.Path == path || pathTemplateMatch(op.Path, path) {
			return true
		}
	}
	return false
}

func pathTemplateMatch(template, actual string) bool {
	t := strings.Split(strings.Trim(template, "/"), "/")
	a := strings.Split(strings.Trim(actual, "/"), "/")
	if len(t) != len(a) {
		return false
	}
	for i := range t {
		if strings.HasPrefix(t[i], "{") && strings.HasSuffix(t[i], "}") {
			if a[i] == "" {
				return false
			}
			continue
		}
		if t[i] != a[i] {
			return false
		}
	}
	return true
}

type TagGroup struct {
	Name string
	Ops  []Operation
}

func TagGroups(ops []Operation) []TagGroup {
	byTag := make(map[string][]Operation)
	for _, op := range ops {
		for _, tag := range op.Tags {
			byTag[tag] = append(byTag[tag], op)
		}
	}
	names := make([]string, 0, len(byTag))
	for name := range byTag {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i] == "Untagged" {
			return false
		}
		if names[j] == "Untagged" {
			return true
		}
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	groups := make([]TagGroup, 0, len(names))
	for _, name := range names {
		groups = append(groups, TagGroup{Name: name, Ops: byTag[name]})
	}
	return groups
}

// --- Parameters, bodies, responses, security ---

type Parameter struct {
	Name        string
	In          string
	Required    bool
	Description string
	Schema      any
}

func (s *Spec) Params(op Operation) []Parameter {
	item := asMap(asMap(s.raw["paths"])[op.Path])
	var out []Parameter
	for _, source := range []any{item["parameters"], op.Op["parameters"]} {
		for _, raw := range asSlice(source) {
			p := s.derefMap(raw)
			if p == nil {
				continue
			}
			out = append(out, Parameter{
				Name:        asString(p["name"]),
				In:          asString(p["in"]),
				Required:    asBool(p["required"]),
				Description: asString(p["description"]),
				Schema:      p["schema"],
			})
		}
	}
	return out
}

func (s *Spec) RequestBody(op Operation) map[string]any {
	return s.derefMap(op.Op["requestBody"])
}

func (s *Spec) Responses(op Operation) map[string]any {
	return asMap(op.Op["responses"])
}

func (s *Spec) Response(op Operation, code string) map[string]any {
	return s.derefMap(s.Responses(op)[code])
}

func (s *Spec) Security(op Operation) []any {
	return asSlice(op.Op["security"])
}

func (s *Spec) SecuritySchemes() map[string]any {
	return asMap(asMap(s.raw["components"])["securitySchemes"])
}

// AuthHeaders returns the apiKey header names declared by the spec.
func (s *Spec) AuthHeaders() (nameHeader, secretHeader string) {
	nameHeader, secretHeader = "X-SDTD-API-TOKENNAME", "X-SDTD-API-SECRET" // #nosec G101 -- header names, not credentials
	for key, raw := range s.SecuritySchemes() {
		scheme := asMap(raw)
		if asString(scheme["type"]) != "apiKey" || asString(scheme["in"]) != "header" {
			continue
		}
		name := asString(scheme["name"])
		if name == "" {
			continue
		}
		lower := strings.ToLower(key)
		switch {
		case strings.Contains(lower, "secret"):
			secretHeader = name
		case strings.Contains(lower, "name"):
			nameHeader = name
		}
	}
	return nameHeader, secretHeader
}

// --- Refs and schema helpers ---

func (s *Spec) Resolve(ref string) any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	var current any = s.raw
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch node := current.(type) {
		case map[string]any:
			current = node[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(node) {
				return nil
			}
			current = node[index]
		default:
			return nil
		}
		if current == nil {
			return nil
		}
	}
	return current
}

func (s *Spec) derefMap(v any) map[string]any {
	m := asMap(v)
	for depth := 0; m != nil && depth < 8; depth++ {
		ref := asString(m["$ref"])
		if ref == "" {
			return m
		}
		m = asMap(s.Resolve(ref))
	}
	return m
}

func (s *Spec) SchemaTypeLabel(v any) string {
	m := asMap(v)
	if m == nil {
		return "—"
	}
	if ref := asString(m["$ref"]); ref != "" {
		return lastSegment(ref)
	}
	switch t := m["type"].(type) {
	case string:
		return t
	case []any:
		var parts []string
		for _, item := range t {
			if name := asString(item); name != "" {
				parts = append(parts, name)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " | ")
		}
	}
	if m["oneOf"] != nil || m["anyOf"] != nil || m["allOf"] != nil {
		return "composite"
	}
	if len(asSlice(m["enum"])) > 0 {
		return "enum"
	}
	return "—"
}

// ExampleFromSchema builds a representative value for a schema.
func (s *Spec) ExampleFromSchema(v any) any {
	return s.example(v, 0, map[string]bool{})
}

func (s *Spec) example(v any, depth int, seen map[string]bool) any {
	if depth > 8 {
		return nil
	}
	m := asMap(v)
	if m == nil {
		return nil
	}
	if ref := asString(m["$ref"]); ref != "" {
		if seen[ref] {
			return map[string]any{}
		}
		seen[ref] = true
		return s.example(s.Resolve(ref), depth+1, seen)
	}
	if ex, ok := m["example"]; ok {
		return ex
	}
	if ex, ok := m["default"]; ok {
		return ex
	}
	if ex, ok := m["const"]; ok {
		return ex
	}
	if examples := asSlice(m["examples"]); len(examples) > 0 {
		return examples[0]
	}
	if enum := asSlice(m["enum"]); len(enum) > 0 {
		return enum[0]
	}
	if one := asSlice(m["oneOf"]); len(one) > 0 {
		return s.example(one[0], depth+1, seen)
	}
	if anyOf := asSlice(m["anyOf"]); len(anyOf) > 0 {
		return s.example(anyOf[0], depth+1, seen)
	}
	if allOf := asSlice(m["allOf"]); len(allOf) > 0 {
		merged := map[string]any{}
		for _, sub := range allOf {
			if value, ok := s.example(sub, depth+1, seen).(map[string]any); ok {
				for k, v := range value {
					merged[k] = v
				}
			}
		}
		return merged
	}

	typ := ""
	switch t := m["type"].(type) {
	case string:
		typ = t
	case []any:
		for _, item := range t {
			if name := asString(item); name != "" && name != "null" {
				typ = name
				break
			}
		}
	}
	switch typ {
	case "object":
		out := map[string]any{}
		for name, sub := range asMap(m["properties"]) {
			out[name] = s.example(sub, depth+1, seen)
		}
		return out
	case "array":
		if items, ok := m["items"]; ok {
			return []any{s.example(items, depth+1, seen)}
		}
		return []any{}
	case "integer", "number":
		if min, ok := m["minimum"]; ok {
			return min
		}
		return float64(0)
	case "boolean":
		return true
	case "string":
		switch asString(m["format"]) {
		case "date-time":
			return "2026-01-01T00:00:00Z"
		case "uuid":
			return "00000000-0000-0000-0000-000000000000"
		}
		if asString(m["pattern"]) != "" {
			return ""
		}
		return "string"
	}
	return nil
}

// --- Small generic helpers ---

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func lastSegment(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

func prettyJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func exampleString(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case int:
		return strconv.Itoa(value)
	default:
		return prettyJSON(value)
	}
}
