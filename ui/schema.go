package ui

import (
	"strings"

	g "maragu.dev/gomponents"
	//lint:ignore ST1001 gomponents HTML DSL reads better with a dot import
	. "maragu.dev/gomponents/html"
)

// SchemaView renders an OpenAPI schema as a collapsible tree.
func SchemaView(spec *Spec, schema any) g.Node {
	return Div(Class("schema-viewer"), schemaNode(spec, schema, map[string]bool{}, 0))
}

func schemaNode(spec *Spec, schema any, visited map[string]bool, depth int) g.Node {
	if depth > 10 {
		return Span(Class("empty"), g.Text("…"))
	}
	m := asMap(schema)
	if m == nil {
		return Span(Class("empty"), g.Text("no schema"))
	}

	if ref := asString(m["$ref"]); ref != "" {
		parts := []g.Node{
			Span(Class("type-badge type-ref"), Title(ref), g.Text(lastSegment(ref))),
		}
		if !visited[ref] {
			if resolved := spec.Resolve(ref); resolved != nil {
				next := copyVisited(visited)
				next[ref] = true
				parts = append(parts, Div(Class("schema-details"), schemaNode(spec, resolved, next, depth+1)))
			}
		}
		return g.Group(parts)
	}

	for _, keyword := range []string{"oneOf", "anyOf", "allOf"} {
		subs := asSlice(m[keyword])
		if len(subs) == 0 {
			continue
		}
		children := make([]g.Node, 0, len(subs)*2)
		for _, sub := range subs {
			children = append(children, schemaNode(spec, sub, visited, depth+1), Div(Class("schema-gap")))
		}
		return Details(Class("collapsible"), Open(),
			Summary(g.Text(keyword)),
			Div(Class("schema-details"), g.Group(children)),
		)
	}

	parts := []g.Node{typeBadges(m)}

	if props := asMap(m["properties"]); len(props) > 0 {
		required := stringSet(asSlice(m["required"]))
		rows := make([]g.Node, 0, len(props))
		for _, name := range sortedKeys(props) {
			sub := asMap(props[name])
			row := []g.Node{
				Div(Class("schema-row"),
					Span(Class("prop-name"), g.Text(name)),
					g.If(required[name], Span(Class("prop-required"), g.Text("*"))),
					Span(Class("schema-colon"), g.Text(":")),
					Span(Class("schema-type"), schemaNode(spec, props[name], visited, depth+1)),
				),
			}
			if description := asString(sub["description"]); description != "" {
				row = append(row, Div(Class("prop-desc"), g.Text(description)))
			}
			if examples := asSlice(sub["examples"]); len(examples) > 0 {
				row = append(row, Div(Class("prop-desc"), g.Text("example: "), Code(g.Text(exampleString(examples[0])))))
			}
			rows = append(rows, g.Group(row))
		}
		parts = append(parts, Div(Class("schema-details"), g.Group(rows)))
	}

	if items, ok := m["items"]; ok && items != nil {
		parts = append(parts, Div(Class("schema-details"),
			Span(Class("param-desc"), g.Text("items:")),
			g.Text(" "),
			schemaNode(spec, items, visited, depth+1),
		))
	}

	if enum := asSlice(m["enum"]); len(enum) > 0 {
		values := make([]string, 0, len(enum))
		for _, value := range enum {
			values = append(values, exampleString(value))
		}
		parts = append(parts, Div(Class("prop-desc"), g.Text("enum: "+strings.Join(values, ", "))))
	}

	if pattern := asString(m["pattern"]); pattern != "" {
		parts = append(parts, Div(Class("prop-desc"), g.Text("pattern: "), Code(g.Text(pattern))))
	}

	if minLength, ok := m["minLength"]; ok {
		parts = append(parts, Div(Class("prop-desc"), g.Text("minLength: "+exampleString(minLength))))
	}
	if maxLength, ok := m["maxLength"]; ok {
		parts = append(parts, Div(Class("prop-desc"), g.Text("maxLength: "+exampleString(maxLength))))
	}
	if minimum, ok := m["minimum"]; ok {
		parts = append(parts, Div(Class("prop-desc"), g.Text("min: "+exampleString(minimum))))
	}
	if maximum, ok := m["maximum"]; ok {
		parts = append(parts, Div(Class("prop-desc"), g.Text("max: "+exampleString(maximum))))
	}

	if example, ok := m["example"]; ok {
		parts = append(parts, Div(Class("example"), g.Text(prettyJSON(example))))
	}

	return g.Group(parts)
}

func typeBadges(m map[string]any) g.Node {
	types := schemaTypes(m)
	if len(types) == 0 {
		return Span(Class("type-badge type-null"), g.Text("unknown"))
	}
	return g.Group(g.Map(types, func(name string) g.Node {
		return Span(Class("type-badge "+typeClass(name)), g.Text(name))
	}))
}

func schemaTypes(m map[string]any) []string {
	switch t := m["type"].(type) {
	case string:
		if t != "" {
			return []string{t}
		}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if name := asString(item); name != "" {
				out = append(out, name)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if len(asSlice(m["enum"])) > 0 {
		return []string{"enum"}
	}
	if len(asMap(m["properties"])) > 0 {
		return []string{"object"}
	}
	if items, ok := m["items"]; ok && items != nil {
		return []string{"array"}
	}
	return nil
}

func typeClass(name string) string {
	switch name {
	case "object":
		return "type-object"
	case "array":
		return "type-array"
	case "string", "file":
		return "type-string"
	case "integer":
		return "type-integer"
	case "number":
		return "type-number"
	case "boolean":
		return "type-boolean"
	case "null":
		return "type-null"
	case "enum":
		return "type-enum"
	default:
		return "type-ref"
	}
}

func copyVisited(visited map[string]bool) map[string]bool {
	out := make(map[string]bool, len(visited)+1)
	for key, value := range visited {
		out[key] = value
	}
	return out
}

func stringSet(values []any) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if name := asString(value); name != "" {
			out[name] = true
		}
	}
	return out
}
