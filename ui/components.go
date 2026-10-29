package ui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	g "maragu.dev/gomponents"
	"maragu.dev/gomponents/components"
	//lint:ignore ST1001 gomponents HTML DSL reads better with a dot import
	. "maragu.dev/gomponents/html"
)

// ProxyResult is the JSON shape returned by /api/proxy and rendered by the
// try-it panel.
type ProxyResult struct {
	Status     int                 `json:"status"`
	StatusText string              `json:"statusText"`
	Headers    map[string][]string `json:"headers"`
	Body       string              `json:"body"`
	Binary     bool                `json:"binary"`
	DurationMs int64               `json:"durationMs"`
	Error      string              `json:"error,omitempty"`
}

// TryItOutcome carries the rendered result of a try-it request so the operation
// view can include it in the no-JavaScript fallback.
type TryItOutcome struct {
	Result ProxyResult
	Curl   string
}

// Page renders the complete viewer document. When outcome is non-nil the
// operation's try-it result is rendered inline so the page works without JS.
func Page(spec *Spec, baseURL, method, path, query string, state State, outcome *TryItOutcome) g.Node {
	nameHeader, secretHeader := spec.AuthHeaders()
	return components.HTML5(components.HTML5Props{
		Title:    spec.Title() + " · API Docs",
		Language: "en",
		Head: g.Group{
			Meta(Name("htmx-config"), Content(`{"includeIndicatorStyles":false}`)),
			Link(Rel("stylesheet"), Href("/static/style.css?v=11")),
			Link(Rel("icon"), Href("data:,")),
			Script(Src("/static/htmx.min.js?v=2.0.11"), Defer()),
			Script(Src("/static/app.js?v=6"), Defer()),
		},
		Body: g.Group{
			Div(ID("app"),
				Data("name-header", nameHeader),
				Data("secret-header", secretHeader),
				A(Class("skip-link"), Href("#content"), g.Text("Skip to content")),
				Div(ID("progress"), Class("progress"),
					Div(Class("progress-bar"), Aria("hidden", "true")), Aria("hidden", "true")),
				Div(ID("toast"), Class("toast"),
					Role("alert"), Aria("live", "polite"), Hidden(""),
					Span(Class("toast-message")),
					Button(Class("toast-action"), Type("button"), Hidden(""), g.Text("Retry")),
					Button(Class("toast-close"), Type("button"), Aria("label", "Dismiss"), g.Text("×")),
				),
				headerBar(spec, baseURL),
				Div(Class("body"),
					sidebarPanel(spec, query, method, path, state),
					Main(ID("content"), Class("content"), TabIndex("-1"), ContentView(spec, method, path, state, outcome)),
				),
				authDialog(nameHeader, secretHeader),
			),
		},
	})
}

// Notice renders an inline error message for htmx targets.
func Notice(message string) g.Node {
	return Div(Class("tryit-error"), g.Text(message))
}

// SidebarNotice renders an inline error inside the sidebar target.
func SidebarNotice(message string) g.Node {
	return Div(Class("no-results"), g.Text(message))
}

// ErrorPage renders a standalone failure page with a retry action.
func ErrorPage(title, message string) g.Node {
	return components.HTML5(components.HTML5Props{
		Title:    title,
		Language: "en",
		Head: g.Group{
			Link(Rel("stylesheet"), Href("/static/style.css?v=11")),
			Link(Rel("icon"), Href("data:,")),
		},
		Body: g.Group{
			Div(ID("app"),
				Div(Class("loading-screen error-screen"),
					H2(g.Text(title)),
					P(g.Text(message)),
					Form(Method("post"), Action("/ui/reload"),
						Button(Class("btn btn-primary"), Type("submit"), g.Text("Retry")),
					),
				),
			),
		},
	})
}

func headerBar(spec *Spec, baseURL string) g.Node {
	return Header(Class("header"),
		Div(Class("header-title"),
			H1(g.Text(spec.Title())),
			g.If(spec.Version() != "", Span(Class("version-badge"), g.Text("v"+spec.Version()))),
		),
		Div(Class("header-actions"),
			g.If(baseURL != "", Span(Class("server-url"), Title("Requests are proxied to this 7D2D server"), g.Text("Target: "+baseURL))),
			Button(ID("auth-btn"), Class("refresh-btn"), Type("button"),
				Title("Authentication settings"), Aria("label", "Authentication settings"), g.Text("🔑")),
			Form(Method("post"), Action("/ui/reload"),
				g.Attr("hx-post", "/ui/reload"),
				g.Attr("hx-target", "#content"),
				g.Attr("hx-swap", "innerHTML"),
				Button(Class("refresh-btn"), Type("submit"),
					Title("Reload API specification"), Aria("label", "Reload API specification"), g.Text("⟳")),
			),
		),
	)
}

func sidebarPanel(spec *Spec, query, method, path string, state State) g.Node {
	return Aside(Class("sidebar"),
		Form(Class("search-box"), Method("get"), Action("/"), g.Attr("role", "search"),
			Input(Type("search"), ID("search-input"), Name("q"), Value(query),
				Aria("label", "Filter endpoints"),
				Placeholder("Filter endpoints…"), g.Attr("autocomplete", "off"),
				g.Attr("hx-get", "/ui/sidebar"),
				g.Attr("hx-trigger", "input changed delay:150ms, search"),
				g.Attr("hx-target", "#sidebar-content"),
				g.Attr("hx-swap", "innerHTML"),
			),
			Span(Class("search-icon"), g.Text("⌕")),
		),
		SidebarContent(spec, query, method, path, state, false),
	)
}

// SidebarContent renders the sidebar body. When oob is true it carries
// hx-swap-oob so an operation response can refresh the active link.
func SidebarContent(spec *Spec, query, method, path string, state State, oob bool) g.Node {
	attrs := []g.Node{ID("sidebar-content"), Class("sidebar-content")}
	if oob {
		attrs = append(attrs, g.Attr("hx-swap-oob", "innerHTML"))
	}
	attrs = append(attrs, sidebarGroups(spec, query, method, path, state))
	return Div(attrs...)
}

// SidebarInner renders only the sidebar contents, for innerHTML swaps into
// #sidebar-content.
func SidebarInner(spec *Spec, query, method, path string, state State) g.Node {
	return sidebarGroups(spec, query, method, path, state)
}

func sidebarGroups(spec *Spec, query, method, path string, state State) g.Node {
	ops := spec.FilterOperations(query)
	if len(ops) == 0 {
		return Div(Class("no-results"), g.Text("No endpoints match."))
	}

	groups := TagGroups(ops)
	defaultTag := ""
	if len(groups) > 0 {
		defaultTag = groups[0].Name
		for _, group := range groups {
			if group.Name == "Commands" {
				defaultTag = "Commands"
				break
			}
		}
	}

	selectedTags := map[string]bool{}
	if op, ok := spec.FindOperation(method, path); ok {
		for _, tag := range op.Tags {
			selectedTags[tag] = true
		}
	}

	nodes := make([]g.Node, 0, len(groups))
	for _, group := range groups {
		open := state.TagOpen(group.Name, group.Name == defaultTag)
		if query != "" || selectedTags[group.Name] {
			open = true
		}
		persist := "1"
		if query != "" {
			persist = "0"
		}
		nodes = append(nodes, tagGroup(group, open, persist, method, path, query))
	}
	return g.Group(nodes)
}

func tagGroup(group TagGroup, open bool, persist, method, path, query string) g.Node {
	return Details(Class("tag-group"),
		Data("state-kind", "tag"),
		Data("state-name", group.Name),
		Data("persist", persist),
		g.If(open, Open()),
		Summary(Class("tag-header"),
			Span(Class("tag-toggle"), g.Text("▾")),
			Span(Class("tag-name"), g.Text(group.Name)),
			Span(Class("op-count"), g.Text(strconv.Itoa(len(group.Ops)))),
		),
		Div(Class("tag-ops"),
			g.Map(group.Ops, func(op Operation) g.Node {
				return operationLink(op, method, path, query)
			}),
		),
	)
}

func operationLink(op Operation, selectedMethod, selectedPath, query string) g.Node {
	href := operationHref(op.Method, op.Path)
	class := "operation-item"
	active := op.Method == strings.ToLower(selectedMethod) && op.Path == selectedPath
	if active {
		class += " active"
	}
	nodes := []g.Node{
		Class(class),
		Href(href),
		g.If(active, Aria("current", "page")),
		g.Attr("hx-get", "/ui/op?"+operationQuery(op.Method, op.Path, query)),
		g.Attr("hx-target", "#content"),
		g.Attr("hx-swap", "innerHTML"),
		g.Attr("hx-push-url", href),
		Span(Class("method-badge method-"+op.Method), g.Text(strings.ToUpper(op.Method))),
		Span(Class("op-path"), g.Text(op.Path)),
	}
	if summary := op.Summary(); summary != "" {
		nodes = append(nodes, Span(Class("op-summary"), g.Text(summary)))
	}
	return A(nodes...)
}

func operationHref(method, path string) string {
	return "/op?" + operationQuery(method, path, "")
}

func operationQuery(method, path, query string) string {
	values := url.Values{}
	values.Set("method", strings.ToUpper(method))
	values.Set("path", path)
	if query != "" {
		values.Set("q", query)
	}
	return values.Encode()
}

// ContentView renders the main panel for the selected operation, optionally
// including a pre-computed try-it outcome for the no-JavaScript fallback.
func ContentView(spec *Spec, method, path string, state State, outcome *TryItOutcome) g.Node {
	if method == "" || path == "" {
		return emptyState("Select an endpoint from the sidebar.")
	}
	op, ok := spec.FindOperation(method, path)
	if !ok {
		return emptyState("No operation found.")
	}
	return operationView(spec, op, state, outcome)
}

func emptyState(message string) g.Node {
	return Div(Class("content-placeholder"), P(g.Text(message)))
}

func operationView(spec *Spec, op Operation, state State, outcome *TryItOutcome) g.Node {
	children := []g.Node{
		H2(ID("content-title"), TabIndex("-1"),
			Span(Class("method-badge method-"+op.Method), g.Text(strings.ToUpper(op.Method))), g.Text(" "), g.Text(op.Path)),
		Div(Class("op-meta"),
			g.If(op.OperationID() != "", Span(Class("op-id"), g.Text(op.OperationID()))),
			g.If(op.Deprecated(), Span(Class("tag-pill deprecated-pill"), g.Text("deprecated"))),
		),
	}
	if len(op.Tags) > 0 {
		children = append(children, Div(Class("tags-container"),
			g.Map(op.Tags, func(tag string) g.Node {
				return Span(Class("tag-pill"), g.Text(tag))
			}),
		))
	}
	if op.Summary() != "" {
		children = append(children, H3(Class("op-summary-title"), g.Text(op.Summary())))
	}
	if op.Description() != "" {
		children = append(children, Div(Class("op-description"), g.Text(op.Description())))
	}

	params := spec.Params(op)
	if len(params) > 0 {
		children = append(children, parametersSection(spec, params, state))
	}
	if requestBody := spec.RequestBody(op); requestBody != nil {
		children = append(children, requestBodySection(spec, requestBody, state))
	}
	children = append(children, tryItSection(spec, op, params, state, outcome))
	if responses := spec.Responses(op); len(responses) > 0 {
		children = append(children, responsesSection(spec, op, state))
	}
	if security := spec.Security(op); len(security) > 0 {
		children = append(children, securitySection(spec, security, state))
	}

	return Article(Class("op-detail"), g.Group(children))
}

func section(name, title string, state State, body ...g.Node) g.Node {
	return Details(Class("section"),
		Data("state-kind", "section"),
		Data("state-name", name),
		g.If(state.SectionOpen(name), Open()),
		Summary(Class("section-header"), g.Text(title)),
		Div(Class("section-body"), g.Group(body)),
	)
}

func parametersSection(spec *Spec, params []Parameter, state State) g.Node {
	return section("parameters", "Parameters", state,
		Table(Class("param-table"),
			THead(Tr(
				Th(g.Text("Name")),
				Th(g.Text("In")),
				Th(g.Text("Type")),
				Th(g.Text("Required")),
				Th(g.Text("Description")),
			)),
			TBody(g.Map(params, func(param Parameter) g.Node {
				return Tr(
					Td(Class("param-name"), g.Text(param.Name)),
					Td(Span(Class("param-location"), g.Text(param.In))),
					Td(Span(Class("param-type"), g.Text(spec.SchemaTypeLabel(param.Schema)))),
					Td(
						g.If(param.Required, Span(Class("required-marker"), g.Text("required"))),
						g.If(!param.Required, g.Text("No")),
					),
					Td(Class("param-desc"),
						g.If(param.Description != "", g.Text(param.Description)),
						g.If(param.Description == "", Span(Class("empty"), g.Text("—"))),
					),
				)
			})),
		),
	)
}

func requestBodySection(spec *Spec, requestBody map[string]any, state State) g.Node {
	body := []g.Node{}
	if asBool(requestBody["required"]) {
		body = append(body, Span(Class("required-marker"), g.Text("required")), g.Text(" "))
	}

	content := asMap(requestBody["content"])
	for _, mediaType := range sortedKeys(content) {
		media := asMap(content[mediaType])
		mediaNodes := []g.Node{Div(Class("media-type-label"), g.Text(mediaType))}
		if schema := media["schema"]; schema != nil {
			mediaNodes = append(mediaNodes, SchemaView(spec, schema))
		}
		if example, ok := media["example"]; ok {
			mediaNodes = append(mediaNodes, Div(Class("example"), g.Text(prettyJSON(example))))
		}
		body = append(body, Div(Class("media-type"), g.Group(mediaNodes)))
	}
	if len(content) == 0 {
		body = append(body, Span(Class("empty"), g.Text("no content")))
	}
	return section("request-body", "Request Body", state, body...)
}

func responsesSection(spec *Spec, op Operation, state State) g.Node {
	responses := spec.Responses(op)
	codes := sortedKeys(responses)
	sort.SliceStable(codes, func(i, j int) bool {
		left, leftErr := strconv.Atoi(codes[i])
		right, rightErr := strconv.Atoi(codes[j])
		if leftErr == nil && rightErr == nil {
			return left < right
		}
		return codes[i] < codes[j]
	})

	items := make([]g.Node, 0, len(codes))
	for _, code := range codes {
		response := spec.Response(op, code)
		itemNodes := []g.Node{
			Div(Class("response-summary"),
				Span(Class("response-code "+responseClass(code)), g.Text(code)),
				Span(Class("response-description"), g.Text(asString(response["description"]))),
			),
		}

		content := asMap(response["content"])
		for _, mediaType := range sortedKeys(content) {
			media := asMap(content[mediaType])
			mediaNodes := []g.Node{Div(Class("media-type-label"), g.Text(mediaType))}
			if schema := media["schema"]; schema != nil {
				mediaNodes = append(mediaNodes, SchemaView(spec, schema))
			}
			if example, ok := media["example"]; ok {
				mediaNodes = append(mediaNodes, Div(Class("example"), g.Text(prettyJSON(example))))
			}
			itemNodes = append(itemNodes, Div(Class("media-type"), g.Group(mediaNodes)))
		}

		headers := asMap(response["headers"])
		if len(headers) > 0 {
			itemNodes = append(itemNodes, Div(Class("media-type-label"), g.Text("Headers")))
			rows := make([]g.Node, 0, len(headers))
			for _, name := range sortedKeys(headers) {
				header := asMap(headers[name])
				rows = append(rows, Tr(
					Td(Class("param-name"), g.Text(name)),
					Td(Span(Class("param-type"), g.Text(spec.SchemaTypeLabel(header["schema"])))),
					Td(Class("param-desc"), g.Text(asString(header["description"]))),
				))
			}
			itemNodes = append(itemNodes, Table(Class("param-table"), TBody(g.Group(rows))))
		}

		items = append(items, Div(Class("response-item"), g.Group(itemNodes)))
	}

	return section("responses", "Responses", state, g.Group(items))
}

func securitySection(spec *Spec, security []any, state State) g.Node {
	schemes := spec.SecuritySchemes()
	badges := []g.Node{}
	for _, requirement := range security {
		for _, name := range sortedKeys(asMap(requirement)) {
			label := name
			if description := asString(asMap(schemes[name])["description"]); description != "" {
				label = name + " (" + description + ")"
			}
			badges = append(badges, Span(Class("security-badge"), Title(label), g.Text(name)))
		}
	}
	return section("security", "Security", state, Div(Class("security-list"), g.Group(badges)))
}

func tryItSection(spec *Spec, op Operation, params []Parameter, state State, outcome *TryItOutcome) g.Node {
	children := []g.Node{
		Input(Type("hidden"), Name("method"), Value(strings.ToUpper(op.Method))),
		Input(Type("hidden"), Name("path"), Value(op.Path)),
	}

	inputs := make([]Parameter, 0, len(params))
	for _, param := range params {
		switch param.In {
		case "path", "query", "header", "cookie":
			if param.Name != "" {
				inputs = append(inputs, param)
			}
		}
	}
	if len(inputs) > 0 {
		fields := make([]g.Node, 0, len(inputs))
		for _, param := range inputs {
			example := exampleString(spec.ExampleFromSchema(param.Schema))
			placeholder := param.Description
			if placeholder == "" {
				placeholder = param.Name + " (" + param.In + ")"
			}
			fields = append(fields, Div(Class("tryit-field"),
				Label(
					g.Text(param.Name),
					g.If(param.Required, Span(Class("prop-required"), g.Text("*"))),
					Span(Class("param-location"), g.Text(param.In)),
				),
				Input(Type("text"),
					Name("param:"+param.In+":"+param.Name),
					Value(example),
					Placeholder(placeholder),
					g.Attr("autocomplete", "off"),
				),
			))
		}
		children = append(children, Div(Class("tryit-grid"), g.Group(fields)))
	}

	if requestBody := spec.RequestBody(op); requestBody != nil {
		content := asMap(requestBody["content"])
		if len(content) > 0 {
			mediaTypes := sortedKeys(content)
			selected := mediaTypes[0]
			required := asBool(requestBody["required"])

			// safeBodyExample returns a non-destructive default body for
			// operations whose spec example could mutate state (e.g. 7D2D's
			// /api/command, whose example runs "settime").
			safeBody := safeBodyExample(op.OperationID())
			exampleFor := func(mediaType string) string {
				if safeBody != "" {
					return safeBody
				}
				return mediaExample(spec, asMap(content[mediaType]))
			}

			if len(mediaTypes) > 1 {
				options := make([]g.Node, 0, len(mediaTypes))
				for _, mediaType := range mediaTypes {
					options = append(options, Option(
						Value(mediaType),
						g.If(mediaType == selected, Selected()),
						Data("example", exampleFor(mediaType)),
						g.Text(mediaType),
					))
				}
				children = append(children,
					Label(Class("tryit-body-label"),
						g.Text("Request body "),
						Select(Class("tryit-media"), Name("content_type"), g.Group(options)),
						g.If(required, Span(Class("prop-required"), g.Text("*"))),
					),
					Textarea(Class("tryit-body"), Name("body"), g.Attr("spellcheck", "false"),
						Placeholder("request body"), g.Text(exampleFor(selected))),
				)
			} else {
				children = append(children,
					Input(Type("hidden"), Name("content_type"), Value(selected)),
					Label(Class("tryit-body-label"),
						g.Text("Request body "),
						Span(Class("param-location"), g.Text(selected)),
						g.If(required, Span(Class("prop-required"), g.Text("*"))),
					),
					Textarea(Class("tryit-body"), Name("body"), g.Attr("spellcheck", "false"),
						Placeholder("request body"), g.Text(exampleFor(selected))),
				)
			}
		}
	}

	children = append(children,
		Div(Class("tryit-actions"),
			Button(Class("btn btn-primary"), Type("submit"), g.Text("Send request")),
			Span(Class("tryit-loading htmx-indicator"), g.Text("Sending…")),
			Span(Class("tryit-hint"), Data("tryit-hint", "1"),
				g.Text("No authentication configured (use 🔑 to add)")),
		),
		Div(ID("tryit-result"), Class("tryit-result"), Aria("live", "polite"),
			resultTryIt(outcome),
		),
	)

	return section("tryit", "Try it out", state,
		Form(Class("tryit-form"),
			Method("post"), Action("/ui/tryit"),
			g.Attr("hx-post", "/ui/tryit"),
			g.Attr("hx-target", "#tryit-result"),
			g.Attr("hx-swap", "innerHTML"),
			g.Attr("hx-disabled-elt", "find button"),
			g.Group(children),
		),
	)
}

// TryItResult renders the outcome of a proxied try-it request. The optional
// curl command is rendered in a copyable block.
func TryItResult(result ProxyResult, curl string) g.Node {
	if result.Error != "" {
		return Div(Class("tryit-error"), g.Text(result.Error))
	}

	summary := []g.Node{
		Span(Class("tryit-status "+tryItStatusClass(result.Status)),
			g.Text(fmt.Sprintf("%d %s", result.Status, result.StatusText))),
		Span(Class("tryit-meta"), g.Text(fmt.Sprintf("%d ms", result.DurationMs))),
	}
	if !result.Binary {
		summary = append(summary, Button(Class("btn btn-ghost tryit-copy"), Type("button"),
			Data("copy-response", "1"), g.Text("Copy response")))
	}
	if curl != "" {
		summary = append(summary, Button(Class("btn btn-ghost tryit-copy"), Type("button"),
			Data("copy-curl", "1"), g.Text("Copy cURL")))
	}

	nodes := []g.Node{
		Div(Class("tryit-summary"), g.Group(summary)),
	}

	if len(result.Headers) > 0 {
		keys := make([]string, 0, len(result.Headers))
		for key := range result.Headers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		rows := make([]g.Node, 0, len(keys))
		for _, key := range keys {
			rows = append(rows, Tr(
				Td(Class("param-name"), g.Text(key)),
				Td(Class("param-desc"), g.Text(strings.Join(result.Headers[key], ", "))),
			))
		}
		nodes = append(nodes, Details(Class("collapsible"),
			Summary(g.Text("Response headers")),
			Div(Class("schema-details"), Table(Class("param-table"), TBody(g.Group(rows)))),
		))
	}

	if result.Binary {
		nodes = append(nodes, Div(Class("tryit-meta"),
			g.Text(fmt.Sprintf("Binary response (%d base64 chars) — not displayed.", len(result.Body)))))
	} else {
		body := result.Body
		if looksLikeJSON(body) || strings.Contains(strings.ToLower(firstHeader(result.Headers, "Content-Type")), "json") {
			var decoded any
			if json.Unmarshal([]byte(body), &decoded) == nil {
				body = prettyJSON(decoded)
			}
		}
		if body == "" {
			body = "(empty body)"
		}
		nodes = append(nodes, Pre(Class("example"), g.Text(body)))
	}

	if curl != "" {
		nodes = append(nodes, Details(Class("collapsible tryit-curl"),
			Summary(g.Text("cURL")),
			Div(Class("schema-details"),
				Pre(Class("example curl"), g.Text(curl)),
			),
		))
	}

	return g.Group(nodes)
}

// mediaExample returns the request body example for one media type.
func mediaExample(spec *Spec, media map[string]any) string {
	example, hasExample := media["example"]
	if !hasExample {
		example = spec.ExampleFromSchema(media["schema"])
	}
	if example == nil {
		return ""
	}
	if text, ok := example.(string); ok {
		return text
	}
	return prettyJSON(example)
}

// safeBodyExamples maps an operationId to a non-destructive request body that
// is used as the try-it default instead of the spec's example when that example
// would mutate server state (e.g. 7D2D's "settime" command). Values must be
// valid for the operation's primary request content type.
var safeBodyExamples = map[string]string{
	"command.post": `{"command":"help * player","format":"Simple"}`,
}

// safeBodyExample returns a safe default body for the operation, if any.
func safeBodyExample(operationID string) string {
	return safeBodyExamples[operationID]
}

// resultTryIt renders the try-it outcome for the result container. It guards
// against a nil outcome so the fragment-swap path does not dereference one.
func resultTryIt(outcome *TryItOutcome) g.Node {
	if outcome == nil {
		return g.Text("")
	}
	return TryItResult(outcome.Result, outcome.Curl)
}

func tryItStatusClass(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "ok"
	case status >= 300 && status < 400:
		return "redirect"
	case status >= 400 && status < 500:
		return "client-error"
	default:
		return "server-error"
	}
}

func responseClass(code string) string {
	value, err := strconv.Atoi(code)
	if err != nil {
		return ""
	}
	switch {
	case value >= 200 && value < 300:
		return "response-2xx"
	case value >= 300 && value < 400:
		return "response-3xx"
	case value >= 400 && value < 500:
		return "response-4xx"
	case value >= 500:
		return "response-5xx"
	default:
		return ""
	}
}

func looksLikeJSON(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func firstHeader(headers map[string][]string, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func authDialog(nameHeader, secretHeader string) g.Node {
	return Dialog(ID("auth-dialog"), Class("modal-overlay"), Aria("label", "Authentication settings"),
		Div(Class("modal"),
			H2(g.Text("Authentication")),
			P(Class("modal-hint"), g.Text("Stored in this browser only. Sent with try-it requests.")),
			Label(Class("modal-field"),
				Span(g.Text("Token name")),
				Input(ID("auth-token-name"), Type("text"), Placeholder(nameHeader), g.Attr("autocomplete", "off")),
			),
			Label(Class("modal-field"),
				Span(g.Text("Token secret")),
				Input(ID("auth-token-secret"), Type("password"), Placeholder(secretHeader), g.Attr("autocomplete", "off")),
			),
			Label(Class("modal-field"),
				Span(g.Text("Session cookie")),
				Input(ID("auth-cookie"), Type("text"), Placeholder("sid=…"), g.Attr("autocomplete", "off")),
			),
			Div(Class("modal-actions"),
				Button(ID("auth-clear"), Class("btn"), Type("button"), g.Text("Clear")),
				Button(ID("auth-close"), Class("btn"), Type("button"), g.Text("Cancel")),
				Button(ID("auth-save"), Class("btn btn-primary"), Type("button"), g.Text("Save")),
			),
		),
	)
}
