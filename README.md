# seven-api

A hand-crafted Go web server that renders the **7 Days To Die WebAPI** OpenAPI
specs in a RapiDoc-style viewer, with authentication and a working **Try it out**
panel.

The UI is server-rendered with [gomponents](https://maragu.dev/gomponents) and
uses a vendored copy of [htmx](https://htmx.org/) 2.0.11 (0BSD, see
`web/htmx.LICENSE`) for partial updates. There are no JS frameworks, no CDN
assets and no code generators.

## How it works

1. On startup the server fetches `openapi.yaml` and every `*.openapi.yaml` it
   references from a running 7D2D server (default `http://localhost:9898`), or
   reads them from a local directory.
2. It merges the per-API documents into a single spec:
   - resolves the `./File.openapi.yaml#/paths/...` references in `paths`
   - merges all `components` (schemas, parameters, responses, requestBodies)
   - rewrites cross-file `$ref`s (`./openapi.yaml#/...`) to local `#/...` refs
   - normalises YAML maps so response codes (`200`, `404`) are string keys
3. It serves the merged spec at `/api/spec` and the viewer at `/` (and `/op`).
4. `/api/proxy` forwards "Try it out" requests to the configured 7D2D server
   (the browser never calls the 7D2D server directly, so there are no CORS
   problems and auth headers can be injected server-side).

Pulling the specs on every start means the viewer always matches the running
server — no stale downloaded copies.

## Usage

```bash
# Pull from http://localhost:9898 and listen on 127.0.0.1:8080 (defaults)
go run .

# Point at another server
go run . -api-url https://192.168.1.10:8080

# Read the YAML from disk instead of a live server
go run . -local-dir ../seven/api

# Build a binary
go build -o server . && ./server
```

Flags:

| Flag | Default | Description |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8080` | Address to listen on |
| `-api-url` | `http://localhost:9898` | 7D2D server base URL (spec source and proxy target) |
| `-local-dir` | *(empty)* | Read YAML from this directory instead of the server |
| `-version` | | Print the build version and exit |

Remote API URLs must use HTTPS. Plain HTTP is allowed only for `localhost` and
loopback IP addresses. The viewer binds to loopback by default; use
`-addr :8080` only when you intend to expose it to a network you trust.

If the 7D2D server is unreachable at startup the viewer still starts and shows a
retry screen; press **Retry** once the server is up. A failed reload keeps
serving the last good spec and reports the error, rather than taking the viewer
down.

For live reload during development, `.air.toml` runs the server via
[Air](https://github.com/air-verse/air) on `127.0.0.1:9999`.

## Features

- Sidebar grouped by tag, with a server-side endpoint filter and deep links
  (`/op?method=GET&path=/api/player/{id}`). Operation summaries appear in a
  hover/focus popover so the narrow sidebar never clips or reflows. Tag-group
  and section open/closed state is kept in an HttpOnly `sdtd_ui` cookie and
  restored on refresh.
- Operation view: parameters, request body, responses, security requirements and
  a recursive schema tree that resolves `$ref`, `oneOf`/`anyOf`/`allOf`, enums,
  patterns and examples.
- **Authentication panel** (🔑): token name and token secret headers (names are
  read from the spec's `securitySchemes`, defaulting to
  `X-SDTD-API-TOKENNAME`/`X-SDTD-API-SECRET`) plus a session cookie. Values are
  stored in `localStorage` and attached by the htmx request hook.
- **Try it out**: auto-generates a request body from the schema, provides inputs
  for path/query/header/cookie parameters, sends the request through the proxy,
  and shows status, timing, headers and pretty-printed JSON. Operations with
  several request media types get a selector, and the result can be copied as
  the response body or as an equivalent cURL command.
- Reload button (⟳) re-pulls the specs from the server without restarting. If
  the reload fails, the last good spec stays available.

## Security

- The listener defaults to `127.0.0.1:8080`.
- Mutating UI/API endpoints reject cross-origin requests and `/api/proxy`
  requires `Content-Type: application/json`.
- The proxy only allows methods and paths declared in the loaded spec, and only
  ever targets the host from `-api-url`.
- Local spec `$ref`s must be bare filenames; files are opened with
  `os.OpenInRoot` and cannot escape `-local-dir`.
- Spec documents are size-limited, empty documents are rejected, and responses
  are sent with a CSP that allows no inline styles, plus `X-Frame-Options`,
  `X-Content-Type-Options`, `Referrer-Policy`, `Cross-Origin-Opener-Policy`,
  `Cross-Origin-Resource-Policy` and `Permissions-Policy` headers.
- JSON API responses are sent with `Cache-Control: no-store`.

## Endpoints

| Path | Purpose |
| --- | --- |
| `/` | Viewer UI (empty selection) |
| `/op?method=GET&path=...` | Viewer UI with an operation selected |
| `/ui/op` | htmx fragment: operation content + sidebar out-of-band swap |
| `/ui/sidebar` | htmx fragment: filtered sidebar |
| `/ui/toggle` | POST: persist tag/section open state in `sdtd_ui` |
| `/ui/tryit` | POST: send a try-it form through the proxy, return a result fragment |
| `/ui/reload` | POST: re-fetch the specs; htmx gets `HX-Refresh: true` |
| `/static/` | Embedded CSS, JS and htmx |
| `/healthz` | Liveness JSON: status, version and whether a spec is loaded |
| `/api/spec` | Merged OpenAPI spec as JSON |
| `/api/reload` | POST: re-fetch and merge the specs |
| `/api/config` | Configured base URL |
| `/api/proxy` | POST: `{method, path, query, body, headers}` → upstream response |

## Development

Tasks are defined in `Taskfile.yml` and run with
[Task](https://taskfile.dev/):

```bash
task build      # build ./server
task run        # run on 127.0.0.1:8080
task check      # go test -race, go vet, staticcheck
task sec        # govulncheck, gosec
task smoke      # smoke-test a running viewer (default http://127.0.0.1:8080)
```

The same checks run in GitHub Actions (`.github/workflows/ci.yml`). The server
shuts down gracefully on `SIGINT`/`SIGTERM`, giving in-flight requests up to ten
seconds to finish.
