# HTTP Transport Support via Connect Protocol

**Date:** 2026-06-10  
**Status:** Approved

## Problem

Inside a secure sandbox, outbound traffic goes through a proxy that auto-injects the `Authorization` header. gRPC uses HTTP/2 binary framing with gRPC-specific metadata, which most HTTP proxies cannot manipulate. HTTP (Connect protocol) uses standard HTTP headers, making proxy-based auth injection possible.

NWEB APIs support both gRPC and Connect protocol on the same endpoint. The CLI needs a way to switch wire protocol without changing auth logic.

## Solution

Migrate all client code from gRPC-generated clients to Connect-generated clients (`connectrpc.com/connect`). Connect clients speak **gRPC protocol by default** (`connect.WithGRPC()`), so existing behaviour is unchanged. Setting `NWEB_API_TRANSPORT=http` switches to Connect HTTP protocol over standard HTTP/1.1 or HTTP/2.

Auth is unchanged: the CLI still manages JWT acquisition and attaches `Authorization: Bearer <token>` to all requests.

## Architecture

### Transport selection

`NWEB_API_TRANSPORT=http` is the only activation mechanism — no CLI flag. Read once in `main.go`'s `PersistentPreRunE` and stored in `flags.Global.Transport`. Any value other than `"http"` (including empty) uses gRPC protocol silently.

### `internal/client/connect.go` (replaces `grpc.go`)

Three exported functions:

```go
// New returns an *http.Client that injects Authorization: Bearer <jwt> on every request.
// If jwt is empty, no Authorization header is set (proxy-injected auth cases).
// insecure=true sets InsecureSkipVerify and uses http:// scheme in BaseURL.
func New(jwt string, insecure bool) *http.Client

// BaseURL converts a gRPC-style endpoint to an HTTPS base URL.
//   "api.nweb.app:443"  → "https://api.nweb.app"
//   "api.nweb.app:8080" → "https://api.nweb.app:8080"
//   "localhost:8080"    → "http://localhost:8080"  (when insecure=true)
func BaseURL(endpoint string, insecure bool) string

// Options returns connect.ClientOptions for the given transport.
//   "http" → []connect.ClientOption{}            (Connect protocol)
//   else   → []connect.ClientOption{connect.WithGRPC()}
func Options(transport string) []connect.ClientOption
```

Auth injection is a thin `http.RoundTripper` wrapper that sets the header and delegates to `http.DefaultTransport` (or a TLS-skipping transport when insecure).

### Updated `connect()` pattern per service

```go
func connect(gf *flags.Global) (taskv1connect.TaskServiceClient, func(), error) {
    path := gf.ConfigPath
    if path == "" { path = config.DefaultConfigPath() }
    cfg, err := config.Load(path)
    if err != nil { return nil, nil, err }
    profile := cfg.ActiveProfileData(gf.Profile)
    resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
    jwt, err := resolver.Token(context.Background())
    if err != nil { return nil, nil, err }
    httpClient := client.New(jwt, gf.Insecure)
    baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
    return taskv1connect.NewTaskServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}
```

The close func is a no-op — HTTP clients have no persistent connection to close.

### Response access

All method calls change uniformly:

```go
// Before (gRPC)
resp, err := svc.ListTasks(ctx, &taskv1.ListTasksRequest{...})
resp.Tasks

// After (Connect)
resp, err := svc.ListTasks(ctx, connect.NewRequest(&taskv1.ListTasksRequest{...}))
resp.Msg.Tasks
```

`*connect.Response[T]` is returned regardless of wire protocol (gRPC or Connect HTTP), so this pattern is identical for both transports — no conditional handling.

### PAT exchange isolation

`auth/token.go`'s `exchangePAT` always uses `connect.WithGRPC()` regardless of `gf.Transport`. The PAT exchange is an internal auth operation that does not go through the proxy injection path.

## Files Changed

| File | Change |
|---|---|
| `buf.gen.yaml` | Add `buf.build/connectrpc/go:v1.16.0` plugin |
| `.bin/build_proto.sh` | No change — `buf generate` picks up new plugins from `buf.gen.yaml` automatically |
| `internal/client/grpc.go` | Replace with `connect.go` |
| `internal/flags/flags.go` | Add `Transport string` field |
| `cmd/retask/main.go` | Populate `gf.Transport` from `NWEB_API_TRANSPORT` in `PersistentPreRunE` |
| `internal/auth/token.go` | Migrate `exchangePAT` to Connect auth client (always gRPC protocol) |
| All 13 service packages | Update `connect()` return type; wrap requests/unwrap responses |

## Edge Cases

- **Unknown `NWEB_API_TRANSPORT` value:** silent fallback to gRPC. Safe for automation.
- **Port 443:** stripped from base URL (`https://` implies 443).
- **Non-standard ports:** preserved in base URL.
- **`--insecure`:** produces `http://` scheme in `BaseURL` and `InsecureSkipVerify` in HTTP client. Behaviour unchanged.
- **Empty JWT:** auth transport skips the header (proxy handles injection). No error.

## Testing

**`internal/client/connect_test.go`:**
- `BaseURL`: port 443 dropped, non-standard port preserved, insecure → `http://`, existing scheme handled
- Auth transport: sets `Authorization: Bearer <token>`; skips header when JWT is empty
- `Options`: `"http"` returns no gRPC option; anything else returns `connect.WithGRPC()`
