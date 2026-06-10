# HTTP Transport via Connect Protocol — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace all gRPC client usage with Connect protocol clients so the CLI works behind an HTTP proxy (activated via `NWEB_API_TRANSPORT=http`), defaulting to gRPC-compatible behaviour.

**Architecture:** Replace `internal/client/grpc.go` with `connect.go` exposing three functions (`New`, `BaseURL`, `Options`). Add `connectrpc/go` to buf codegen (remove `grpc/go` and `gateway`). Add `Transport string` to `flags.Global` populated from `NWEB_API_TRANSPORT`. Update every service package's `connect()` function and all method call sites.

**Tech Stack:** `connectrpc.com/connect` v1.16+, standard `net/http`, existing proto-gen types.

---

## File Map

| File | Action |
|---|---|
| `internal/client/grpc.go` | Delete |
| `internal/client/connect.go` | Create |
| `internal/client/connect_test.go` | Create |
| `internal/flags/flags.go` | Modify — add `Transport string` |
| `cmd/retask/main.go` | Modify — read `NWEB_API_TRANSPORT` |
| `internal/auth/token.go` | Modify — `exchangePAT` uses Connect client |
| `internal/cmd/auth/command.go` | Modify — 3 inline PAT calls |
| `internal/cmd/agent/command.go` | Modify — `connect()` + 5 call sites |
| `internal/cmd/customer/command.go` | Modify — `connect()` + 4 call sites |
| `internal/cmd/file/command.go` | Modify — `connect()` + 4 call sites |
| `internal/cmd/integration/command.go` | Modify — `connect()` + 6 call sites |
| `internal/cmd/project/command.go` | Modify — `connect()` + 9 call sites |
| `internal/cmd/projectconfig/command.go` | Modify — `connect()` + 3 call sites |
| `internal/cmd/sandbox/command.go` | Modify — `connect()` + 12 call sites |
| `internal/cmd/task/command.go` | Modify — `connect()` + 8 call sites |
| `internal/cmd/workspace/command.go` | Modify — `connect()` + 7 call sites |
| `buf.gen.yaml` | Modify — swap plugins |
| `proto-gen/**/*_grpc.pb.go` | Delete (all, before regenerating) |
| `proto-gen/**/*.pb.gw.go` | Delete (all, before regenerating) |

---

## Recurring patterns

Every service call site follows one of these patterns. Memorise them — every task below is a mechanical application.

**List** (before → after):
```go
// Before
resp, err := svc.GetFoos(ctx, &pkgv1.FoosRequest{...})
resp.Foos

// After
resp, err := svc.GetFoos(ctx, connect.NewRequest(&pkgv1.FoosRequest{...}))
resp.Msg.Foos
```

**Get single** (before → after):
```go
// Before
entity, err := svc.GetFoo(ctx, &commonv1.Id{Id: args[0]})
output.Print(gf.Pretty, entity)

// After
resp, err := svc.GetFoo(ctx, connect.NewRequest(&commonv1.Id{Id: args[0]}))
output.Print(gf.Pretty, resp.Msg)
```

**Set / create** (before → after):
```go
// Before
id, err := svc.SetFoo(ctx, entity)
id.Id

// After
resp, err := svc.SetFoo(ctx, connect.NewRequest(entity))
resp.Msg.Id
```

**Discard** (before → after):
```go
// Before
_, err = svc.DeleteFoo(ctx, &commonv1.Id{Id: args[0]})

// After
_, err = svc.DeleteFoo(ctx, connect.NewRequest(&commonv1.Id{Id: args[0]}))
```

**Read-Modify-Write** (before → after):
```go
// Before
existing, err := svc.GetFoo(ctx, &commonv1.Id{Id: args[0]})
existing.Name = name
id, err := svc.SetFoo(ctx, existing)

// After
existingResp, err := svc.GetFoo(ctx, connect.NewRequest(&commonv1.Id{Id: args[0]}))
existingResp.Msg.Name = name
resp, err := svc.SetFoo(ctx, connect.NewRequest(existingResp.Msg))
resp.Msg.Id
```

---

## Task 1 — Write failing tests for `internal/client/connect.go`

**Files:**
- Create: `internal/client/connect_test.go`

- [ ] **Step 1: Create the test file**

```go
// internal/client/connect_test.go
package client_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nwebxyz/retask-cli/internal/client"
)

func TestBaseURL(t *testing.T) {
	tests := []struct {
		endpoint string
		insecure bool
		want     string
	}{
		{"api.nweb.app:443", false, "https://api.nweb.app"},
		{"api.nweb.app:8080", false, "https://api.nweb.app:8080"},
		{"localhost:8080", true, "http://localhost:8080"},
		{"api.nweb.app", false, "https://api.nweb.app"},
		{"https://api.nweb.app", false, "https://api.nweb.app"},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s/insecure=%v", tc.endpoint, tc.insecure), func(t *testing.T) {
			got := client.BaseURL(tc.endpoint, tc.insecure)
			if got != tc.want {
				t.Errorf("BaseURL(%q, %v) = %q, want %q", tc.endpoint, tc.insecure, got, tc.want)
			}
		})
	}
}

func TestOptionsHTTP(t *testing.T) {
	opts := client.Options("http")
	if len(opts) != 0 {
		t.Errorf("Options(\"http\") returned %d options, want 0", len(opts))
	}
}

func TestOptionsGRPC(t *testing.T) {
	for _, transport := range []string{"", "grpc", "unknown"} {
		opts := client.Options(transport)
		if len(opts) != 1 {
			t.Errorf("Options(%q) returned %d options, want 1", transport, len(opts))
		}
	}
}

func TestNewInjectsAuthHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	c := client.New("my-token", false)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got != "Bearer my-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer my-token")
	}
}

func TestNewSkipsAuthHeaderWhenEmpty(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	c := client.New("", false)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got != "" {
		t.Errorf("Authorization = %q, want empty for empty JWT", got)
	}
}
```

- [ ] **Step 2: Confirm tests fail (functions don't exist yet)**

```bash
go test ./internal/client/...
```

Expected: compile error — `client.BaseURL undefined` (or similar).

---

## Task 2 — Implement `internal/client/connect.go`, delete `grpc.go`

**Files:**
- Create: `internal/client/connect.go`
- Delete: `internal/client/grpc.go`

- [ ] **Step 1: Add connectrpc dependency**

```bash
go get connectrpc.com/connect@latest
```

Expected: `go.mod` and `go.sum` updated.

- [ ] **Step 2: Create `internal/client/connect.go`**

```go
// internal/client/connect.go
package client

import (
	"crypto/tls"
	"net/http"
	"strings"

	"connectrpc.com/connect"
)

type authTransport struct {
	jwt  string
	base http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.jwt != "" {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer "+t.jwt)
	}
	return t.base.RoundTrip(req)
}

// New returns an *http.Client that injects Authorization: Bearer <jwt> on every request.
// Empty jwt skips the header (proxy-injected auth). insecure=true disables TLS verification.
func New(jwt string, insecure bool) *http.Client {
	var base http.RoundTripper = http.DefaultTransport
	if insecure {
		base = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	return &http.Client{Transport: &authTransport{jwt: jwt, base: base}}
}

// BaseURL converts a gRPC-style endpoint to an HTTPS base URL.
// Port 443 is dropped (implied by https://). Non-standard ports are preserved.
// insecure=true produces an http:// scheme.
func BaseURL(endpoint string, insecure bool) string {
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	scheme := "https"
	if insecure {
		scheme = "http"
	}
	if !insecure && strings.HasSuffix(endpoint, ":443") {
		endpoint = strings.TrimSuffix(endpoint, ":443")
	}
	return scheme + "://" + endpoint
}

// Options returns connect.ClientOptions for the given transport string.
// "http" → Connect protocol. Anything else (including "") → gRPC protocol.
func Options(transport string) []connect.ClientOption {
	if transport == "http" {
		return []connect.ClientOption{}
	}
	return []connect.ClientOption{connect.WithGRPC()}
}
```

- [ ] **Step 3: Delete `internal/client/grpc.go`**

```bash
rm internal/client/grpc.go
```

- [ ] **Step 4: Run the tests and confirm they pass**

```bash
go test ./internal/client/...
```

Expected: `ok  github.com/nwebxyz/retask-cli/internal/client`

- [ ] **Step 5: Commit**

```bash
git add internal/client/connect.go internal/client/connect_test.go
git rm internal/client/grpc.go
git commit -m "feat: replace gRPC client factory with Connect-based factory"
```

---

## Task 3 — Update `buf.gen.yaml`, delete old generated files, regenerate

**Files:**
- Modify: `buf.gen.yaml`
- Delete: all `proto-gen/**/*_grpc.pb.go` and `proto-gen/**/*.pb.gw.go`

- [ ] **Step 1: Update `buf.gen.yaml`**

Replace the `plugins` block so it reads:

```yaml
plugins:
  - remote: buf.build/protocolbuffers/go:v1.30.0
    out: proto-gen
    opt: paths=source_relative
  - remote: buf.build/connectrpc/go:v1.16.0
    out: proto-gen
    opt: paths=source_relative
```

(Remove the `buf.build/grpc/go` and `buf.build/grpc-ecosystem/gateway` entries — neither is used in a CLI-only codebase.)

- [ ] **Step 2: Delete old gRPC and gateway generated files**

```bash
find proto-gen -name "*_grpc.pb.go" -delete
find proto-gen -name "*.pb.gw.go" -delete
```

- [ ] **Step 3: Regenerate**

```bash
./.bin/build_proto.sh
```

Expected: new `*.connect.pb.go` files appear alongside `*.pb.go` in each proto-gen package. Confirm with:

```bash
find proto-gen -name "*.connect.pb.go" | sort
```

Expected output includes files like:
```
proto-gen/auth/v1/auth.connect.pb.go
proto-gen/customer/v1/customer.connect.pb.go
proto-gen/file/v1/service.connect.pb.go
proto-gen/integration/v1/integration.connect.pb.go
proto-gen/project/v1/project.connect.pb.go
proto-gen/retask/agent/v1/agent.connect.pb.go
proto-gen/retask/project/v1/project.connect.pb.go
proto-gen/retask/sandbox/v1/sandbox.connect.pb.go
proto-gen/retask/task/v1/task.connect.pb.go
proto-gen/workspace/v1/workspace.connect.pb.go
```

- [ ] **Step 4: Confirm the generated `New*ServiceClient` signatures**

```bash
grep "func New.*ServiceClient" proto-gen/retask/task/v1/task.connect.pb.go
```

Expected (Connect signature, NOT gRPC):
```
func NewTaskServiceClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) TaskServiceClient
```

- [ ] **Step 5: Commit**

```bash
git add buf.gen.yaml proto-gen/
git commit -m "chore: replace grpc/go and gateway codegen with connectrpc/go"
```

---

## Task 4 — Add `Transport` to `flags.Global` and read `NWEB_API_TRANSPORT`

**Files:**
- Modify: `internal/flags/flags.go`
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Add `Transport` field to `flags.Global`**

In `internal/flags/flags.go`, add `Transport string` to the struct:

```go
// Global holds persistent flags available on every command.
type Global struct {
	Profile     string
	WorkspaceID string
	Pretty      bool
	Insecure    bool
	NoSave      bool
	ConfigPath  string
	Transport   string
}
```

- [ ] **Step 2: Populate `Transport` from env in `PersistentPreRunE`**

In `cmd/retask/main.go`, inside `PersistentPreRunE`, add after the existing env reads:

```go
if gf.Transport == "" {
    gf.Transport = os.Getenv("NWEB_API_TRANSPORT")
}
```

The full `PersistentPreRunE` block becomes:

```go
root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
    if gf.Profile == "" {
        gf.Profile = os.Getenv("RETASK_PROFILE")
    }
    if os.Getenv("RETASK_NO_PERSIST") != "" {
        gf.NoSave = true
    }
    if gf.Transport == "" {
        gf.Transport = os.Getenv("NWEB_API_TRANSPORT")
    }

    configPath := gf.ConfigPath
    if configPath == "" {
        configPath = config.DefaultConfigPath()
    }
    if cfg, err := config.Load(configPath); err == nil {
        profile := cfg.ActiveProfileData(gf.Profile)
        gf.WorkspaceID = flags.ResolveWorkspaceID(gf.WorkspaceID, profile)
    } else {
        gf.WorkspaceID = flags.ResolveWorkspaceID(gf.WorkspaceID, config.Profile{})
    }

    return nil
}
```

- [ ] **Step 3: Build to verify no compilation errors**

```bash
go build ./...
```

Expected: errors from all service packages that still use the old `client.New(endpoint, jwt, insecure)` signature. This is expected — we haven't migrated those yet.

- [ ] **Step 4: Commit**

```bash
git add internal/flags/flags.go cmd/retask/main.go
git commit -m "feat: add Transport field to flags.Global, read NWEB_API_TRANSPORT env var"
```

---

## Task 5 — Migrate `internal/auth/token.go` (`exchangePAT`)

**Files:**
- Modify: `internal/auth/token.go`

- [ ] **Step 1: Replace `exchangePAT` implementation**

Replace the entire `exchangePAT` method and update imports. The new method does NOT use `grpc.NewClient` — it uses Connect with the `client` package.

Remove these imports:
```go
"google.golang.org/grpc"
"google.golang.org/grpc/credentials"
"google.golang.org/grpc/credentials/insecure"
```

Add these imports:
```go
"connectrpc.com/connect"
"github.com/nwebxyz/retask-cli/internal/client"
```

Replace the `exchangePAT` method body:

```go
func (r *Resolver) exchangePAT(ctx context.Context, pat, workspaceID string) (string, time.Time, error) {
	// PAT exchange never carries a JWT header — the PAT is in the request body.
	// Always use gRPC protocol regardless of NWEB_API_TRANSPORT (internal auth call).
	httpClient := client.New("", r.Insecure)
	baseURL := client.BaseURL(r.Profile.Endpoint, r.Insecure)
	authClient := authv1.NewAuthServiceClient(httpClient, baseURL, connect.WithGRPC())
	resp, err := authClient.ExchangePat(ctx, connect.NewRequest(&authv1.PatExchangeRequest{
		Token:       pat,
		WorkspaceId: workspaceID,
	}))
	if err != nil {
		return "", time.Time{}, err
	}
	var expiresAt time.Time
	if resp.Msg.ExpiresAt != nil {
		expiresAt = resp.Msg.ExpiresAt.AsTime()
	}
	return resp.Msg.Jwt, expiresAt, nil
}
```

- [ ] **Step 2: Build to verify**

```bash
go build ./internal/auth/...
```

Expected: no errors from this package.

- [ ] **Step 3: Commit**

```bash
git add internal/auth/token.go
git commit -m "feat: migrate PAT exchange from grpc.NewClient to Connect client"
```

---

## Task 6 — Migrate `internal/cmd/auth/command.go` (inline PAT calls)

**Files:**
- Modify: `internal/cmd/auth/command.go`

This package doesn't use a shared `connect()` helper — it inlines client creation in three command functions. Each follows the same pattern.

- [ ] **Step 1: Update imports**

Remove: `"google.golang.org/grpc"` (not present — nothing to remove here from the package-level import)
Add: `"connectrpc.com/connect"`

The existing `"github.com/nwebxyz/retask-cli/internal/client"` import stays.

- [ ] **Step 2: Migrate `newPatListCommand`**

Replace the inline client block in `newPatListCommand.RunE`:

Before:
```go
profile, _, _ := loadProfile(gf)
conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
if err != nil {
    return err
}
defer conn.Close()
resp, err := authv1.NewAuthServiceClient(conn).GetPats(context.Background(), &authv1.PatsRequest{})
if err != nil {
    return err
}
return output.Print(gf.Pretty, resp.Pats)
```

After:
```go
profile, _, _ := loadProfile(gf)
httpClient := client.New(jwt, gf.Insecure)
baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
resp, err := authv1.NewAuthServiceClient(httpClient, baseURL, client.Options(gf.Transport)...).GetPats(
    context.Background(), connect.NewRequest(&authv1.PatsRequest{}))
if err != nil {
    return err
}
return output.Print(gf.Pretty, resp.Msg.Pats)
```

- [ ] **Step 3: Migrate `newPatCreateCommand`**

Before:
```go
profile, _, _ := loadProfile(gf)
conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
if err != nil {
    return err
}
defer conn.Close()
resp, err := authv1.NewAuthServiceClient(conn).CreatePat(context.Background(), req)
if err != nil {
    return err
}
return output.Print(gf.Pretty, map[string]any{
    "pat":       resp.Pat,
    "raw_token": resp.RawToken,
})
```

After:
```go
profile, _, _ := loadProfile(gf)
httpClient := client.New(jwt, gf.Insecure)
baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
resp, err := authv1.NewAuthServiceClient(httpClient, baseURL, client.Options(gf.Transport)...).CreatePat(
    context.Background(), connect.NewRequest(req))
if err != nil {
    return err
}
return output.Print(gf.Pretty, map[string]any{
    "pat":       resp.Msg.Pat,
    "raw_token": resp.Msg.RawToken,
})
```

- [ ] **Step 4: Migrate `newPatRevokeCommand`**

Before:
```go
profile, _, _ := loadProfile(gf)
conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
if err != nil {
    return err
}
defer conn.Close()
_, err = authv1.NewAuthServiceClient(conn).RevokePat(
    context.Background(),
    &commonv1.Id{Id: args[0]},
)
```

After:
```go
profile, _, _ := loadProfile(gf)
httpClient := client.New(jwt, gf.Insecure)
baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
_, err = authv1.NewAuthServiceClient(httpClient, baseURL, client.Options(gf.Transport)...).RevokePat(
    context.Background(), connect.NewRequest(&commonv1.Id{Id: args[0]}))
```

- [ ] **Step 5: Build to verify**

```bash
go build ./internal/cmd/auth/...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/auth/command.go
git commit -m "feat: migrate auth PAT commands to Connect client"
```

---

## Task 7 — Migrate `internal/cmd/agent/command.go`

**Files:**
- Modify: `internal/cmd/agent/command.go`

- [ ] **Step 1: Update imports**

Add `"connectrpc.com/connect"`. Remove the gRPC-generated import alias if present (`grpc` package — not present directly, `google.golang.org/grpc` is not imported here).

- [ ] **Step 2: Replace `connect()` function**

```go
func connect(gf *flags.Global) (agentv1.AgentServiceClient, func(), error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return nil, nil, err
	}
	httpClient := client.New(jwt, gf.Insecure)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return agentv1.NewAgentServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}
```

- [ ] **Step 3: Update all call sites**

Apply the recurring patterns to every call site:

`newListCommand` — List pattern:
```go
// Before
resp, err := svc.GetAgents(context.Background(), &agentv1.AgentsRequest{...})
return output.Print(gf.Pretty, resp.Agents)
// After
resp, err := svc.GetAgents(context.Background(), connect.NewRequest(&agentv1.AgentsRequest{...}))
return output.Print(gf.Pretty, resp.Msg.Agents)
```

`newGetCommand` — Get single pattern:
```go
// Before
agent, err := svc.GetAgent(context.Background(), &commonv1.Id{Id: args[0]})
return output.Print(gf.Pretty, agent)
// After
resp, err := svc.GetAgent(context.Background(), connect.NewRequest(&commonv1.Id{Id: args[0]}))
return output.Print(gf.Pretty, resp.Msg)
```

`newCreateCommand` — Set pattern:
```go
// Before
id, err := svc.SetAgent(context.Background(), agent)
return output.Print(gf.Pretty, map[string]string{"agent_id": id.Id})
// After
resp, err := svc.SetAgent(context.Background(), connect.NewRequest(agent))
return output.Print(gf.Pretty, map[string]string{"agent_id": resp.Msg.Id})
```

`newUpdateCommand` — Read-Modify-Write pattern:
```go
// Before
existing, err := svc.GetAgent(context.Background(), &commonv1.Id{Id: args[0]})
// ... modify fields on existing ...
id, err := svc.SetAgent(context.Background(), existing)
return output.Print(gf.Pretty, map[string]string{"agent_id": id.Id})
// After
existingResp, err := svc.GetAgent(context.Background(), connect.NewRequest(&commonv1.Id{Id: args[0]}))
// ... modify fields on existingResp.Msg ...
resp, err := svc.SetAgent(context.Background(), connect.NewRequest(existingResp.Msg))
return output.Print(gf.Pretty, map[string]string{"agent_id": resp.Msg.Id})
```

`newDeleteCommand` — Discard pattern:
```go
// Before
_, err = svc.DeleteAgent(context.Background(), &commonv1.Id{Id: args[0]})
// After
_, err = svc.DeleteAgent(context.Background(), connect.NewRequest(&commonv1.Id{Id: args[0]}))
```

- [ ] **Step 4: Build to verify**

```bash
go build ./internal/cmd/agent/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/agent/command.go
git commit -m "feat: migrate agent commands to Connect client"
```

---

## Task 8 — Migrate `internal/cmd/customer/command.go`

**Files:**
- Modify: `internal/cmd/customer/command.go`

- [ ] **Step 1: Add `"connectrpc.com/connect"` import**

- [ ] **Step 2: Replace `connect()` function**

```go
func connect(gf *flags.Global) (customerv1.CustomerServiceClient, func(), error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return nil, nil, err
	}
	httpClient := client.New(jwt, gf.Insecure)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return customerv1.NewCustomerServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}
```

- [ ] **Step 3: Update all call sites**

`GetMyProfile` — Get single pattern:
```go
// Before: customer, err := svc.GetMyProfile(ctx, &commonv1.Empty{})  →  output.Print(gf.Pretty, customer)
// After:  resp, err := svc.GetMyProfile(ctx, connect.NewRequest(&commonv1.Empty{}))  →  output.Print(gf.Pretty, resp.Msg)
```

Update command (Read-Modify-Write):
```go
// Before: existing, err := svc.GetMyProfile(ctx, &commonv1.Empty{})
//         existing.Field = value
//         id, err := svc.SetMyProfile(ctx, existing)
//         id.Id
// After:  existingResp, err := svc.GetMyProfile(ctx, connect.NewRequest(&commonv1.Empty{}))
//         existingResp.Msg.Field = value
//         resp, err := svc.SetMyProfile(ctx, connect.NewRequest(existingResp.Msg))
//         resp.Msg.Id
```

`GetCustomers` — List pattern:
```go
// Before: resp, err := svc.GetCustomers(ctx, &customerv1.CustomersRequest{})  →  resp.Customers
// After:  resp, err := svc.GetCustomers(ctx, connect.NewRequest(&customerv1.CustomersRequest{}))  →  resp.Msg.Customers
```

`GetCustomer` — Get single:
```go
// Before: customer, err := svc.GetCustomer(ctx, &commonv1.Id{Id: args[0]})  →  output.Print(gf.Pretty, customer)
// After:  resp, err := svc.GetCustomer(ctx, connect.NewRequest(&commonv1.Id{Id: args[0]}))  →  output.Print(gf.Pretty, resp.Msg)
```

- [ ] **Step 4: Build and commit**

```bash
go build ./internal/cmd/customer/...
git add internal/cmd/customer/command.go
git commit -m "feat: migrate customer commands to Connect client"
```

---

## Task 9 — Migrate `internal/cmd/file/command.go`

**Files:**
- Modify: `internal/cmd/file/command.go`

- [ ] **Step 1: Add import, replace `connect()` function**

```go
func connect(gf *flags.Global) (filev1.FileServiceClient, func(), error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return nil, nil, err
	}
	httpClient := client.New(jwt, gf.Insecure)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return filev1.NewFileServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}
```

- [ ] **Step 2: Update all call sites**

`GetFiles` — List pattern: `resp.Files` → `resp.Msg.Files`

`GetFile` — Get single: `f, err := svc.GetFile(...)` → `resp, err := svc.GetFile(...)` + `output.Print(gf.Pretty, resp.Msg)`

`DeleteFile` — Discard: wrap request in `connect.NewRequest`

`GetFileSignedUrl` — List pattern: `resp.` fields → `resp.Msg.` fields

- [ ] **Step 3: Build and commit**

```bash
go build ./internal/cmd/file/...
git add internal/cmd/file/command.go
git commit -m "feat: migrate file commands to Connect client"
```

---

## Task 10 — Migrate `internal/cmd/integration/command.go`

**Files:**
- Modify: `internal/cmd/integration/command.go`

- [ ] **Step 1: Add import, replace `connect()` function**

```go
func connect(gf *flags.Global) (integrationv1.IntegrationServiceClient, func(), error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return nil, nil, err
	}
	httpClient := client.New(jwt, gf.Insecure)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return integrationv1.NewIntegrationServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}
```

- [ ] **Step 2: Update all call sites**

`GetProviders` — List: `resp.Providers` → `resp.Msg.Providers`

`GetProvider` — Get single: `provider, err := ...` → `resp, err := ...` + `output.Print(gf.Pretty, resp.Msg)`

`GetIntegrations` — List: `resp.Integrations` → `resp.Msg.Integrations`

`GetIntegration` — Get single: `intg, err := ...` → `resp, err := ...` + `output.Print(gf.Pretty, resp.Msg)`

`SetIntegration` — Set: `id, err := svc.SetIntegration(ctx, &integrationv1.Integration{...})` → `resp, err := svc.SetIntegration(ctx, connect.NewRequest(&integrationv1.Integration{...}))` + `resp.Msg.Id`

`DeleteIntegration` — Discard: wrap request

`GetGithubRepos` — List: `resp.Repos` → `resp.Msg.Repos`

- [ ] **Step 3: Build and commit**

```bash
go build ./internal/cmd/integration/...
git add internal/cmd/integration/command.go
git commit -m "feat: migrate integration commands to Connect client"
```

---

## Task 11 — Migrate `internal/cmd/project/command.go`

**Files:**
- Modify: `internal/cmd/project/command.go`

- [ ] **Step 1: Add import, replace `connect()` function**

```go
func connect(gf *flags.Global) (projectv1.ProjectServiceClient, func(), error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return nil, nil, err
	}
	httpClient := client.New(jwt, gf.Insecure)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return projectv1.NewProjectServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}
```

- [ ] **Step 2: Update all call sites**

`GetProjects` — List: `resp.Projects` → `resp.Msg.Projects`

`GetProject` (list command + get command) — Get single: `proj, err := ...` → `resp, err := ...` + `output.Print(gf.Pretty, resp.Msg)` and also RMW in update command

`SetProject` (create + update RMW) — Set: `id, err := svc.SetProject(ctx, proj)` → `resp, err := svc.SetProject(ctx, connect.NewRequest(proj))` + `resp.Msg.Id`

Update command — Read-Modify-Write:
```go
// Before
existing, err := svc.GetProject(context.Background(), &commonv1.Id{Id: args[0]})
// modify existing.Field ...
id, err := svc.SetProject(context.Background(), existing)
id.Id

// After
existingResp, err := svc.GetProject(context.Background(), connect.NewRequest(&commonv1.Id{Id: args[0]}))
// modify existingResp.Msg.Field ...
resp, err := svc.SetProject(context.Background(), connect.NewRequest(existingResp.Msg))
resp.Msg.Id
```

`ArchiveProject` — Discard: wrap request
`UnarchiveProject` — Discard: wrap request
`DeleteProject` — Discard: wrap request

`GetProjectMembers` — List: `resp.Members` → `resp.Msg.Members`
`SetProjectMember` — Discard: wrap request
`RemoveProjectMember` — Discard: wrap request

- [ ] **Step 3: Build and commit**

```bash
go build ./internal/cmd/project/...
git add internal/cmd/project/command.go
git commit -m "feat: migrate project commands to Connect client"
```

---

## Task 12 — Migrate `internal/cmd/projectconfig/command.go`

**Files:**
- Modify: `internal/cmd/projectconfig/command.go`

The `projectconfig` package uses `retaskprojectv1.RetaskProjectServiceClient`.

- [ ] **Step 1: Add import, replace `connect()` function**

```go
func connect(gf *flags.Global) (retaskprojectv1.RetaskProjectServiceClient, func(), error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return nil, nil, err
	}
	httpClient := client.New(jwt, gf.Insecure)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return retaskprojectv1.NewRetaskProjectServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}
```

- [ ] **Step 2: Update all call sites**

`GetProjectConfig` — Get single (get command): `cfg, err := ...` → `resp, err := ...` + `output.Print(gf.Pretty, resp.Msg)`

Update command — Read-Modify-Write:
```go
// Before
existing, err := svc.GetProjectConfig(ctx, &commonv1.Id{Id: projectID})
// modify existing.Field ...
id, err := svc.SetProjectConfig(ctx, existing)
id.Id

// After
existingResp, err := svc.GetProjectConfig(ctx, connect.NewRequest(&commonv1.Id{Id: projectID}))
// modify existingResp.Msg.Field ...
resp, err := svc.SetProjectConfig(ctx, connect.NewRequest(existingResp.Msg))
resp.Msg.Id
```

- [ ] **Step 3: Build and commit**

```bash
go build ./internal/cmd/projectconfig/...
git add internal/cmd/projectconfig/command.go
git commit -m "feat: migrate projectconfig commands to Connect client"
```

---

## Task 13 — Migrate `internal/cmd/workspace/command.go`

**Files:**
- Modify: `internal/cmd/workspace/command.go`

- [ ] **Step 1: Add import, replace `connect()` function**

```go
func connect(gf *flags.Global) (workspacev1.WorkspaceServiceClient, func(), error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return nil, nil, err
	}
	httpClient := client.New(jwt, gf.Insecure)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return workspacev1.NewWorkspaceServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}
```

- [ ] **Step 2: Update all call sites**

`GetWorkspaces` — List: `resp.Workspaces` → `resp.Msg.Workspaces`

`GetWorkspace` (get command) — Get single: `ws, err := ...` → `resp, err := ...` + `output.Print(gf.Pretty, resp.Msg)`

`SetWorkspace` (create command — inline not RMW): `id, err := svc.SetWorkspace(ctx, &workspacev1.Workspace{...})` → `resp, err := svc.SetWorkspace(ctx, connect.NewRequest(&workspacev1.Workspace{...}))` + `resp.Msg.Id`

Update command — Read-Modify-Write:
```go
// Before
existing, err := svc.GetWorkspace(ctx, &commonv1.Id{Id: args[0]})
// modify existing.Field ...
id, err := svc.SetWorkspace(ctx, existing)
id.Id

// After
existingResp, err := svc.GetWorkspace(ctx, connect.NewRequest(&commonv1.Id{Id: args[0]}))
// modify existingResp.Msg.Field ...
resp, err := svc.SetWorkspace(ctx, connect.NewRequest(existingResp.Msg))
resp.Msg.Id
```

`DeleteWorkspace` — Discard: wrap request

`GetWorkspaceMembers` — List: `resp.Members` → `resp.Msg.Members`

`InviteWorkspaceMember` — Discard: wrap request
`UpdateWorkspaceMember` — Discard: wrap request
`RemoveWorkspaceMember` — Discard: wrap request

- [ ] **Step 3: Build and commit**

```bash
go build ./internal/cmd/workspace/...
git add internal/cmd/workspace/command.go
git commit -m "feat: migrate workspace commands to Connect client"
```

---

## Task 14 — Migrate `internal/cmd/sandbox/command.go`

**Files:**
- Modify: `internal/cmd/sandbox/command.go`

- [ ] **Step 1: Add import, replace `connect()` function**

```go
func connect(gf *flags.Global) (sandboxv1.SandboxServiceClient, func(), error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return nil, nil, err
	}
	httpClient := client.New(jwt, gf.Insecure)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return sandboxv1.NewSandboxServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}
```

- [ ] **Step 2: Update sandbox call sites**

`GetSandboxes` — List: `resp.Sandboxes` → `resp.Msg.Sandboxes`

`GetSandbox` (get command) — Get single: `sandbox, err := ...` → `resp, err := ...` + `output.Print(gf.Pretty, resp.Msg)`

`SetSandbox` (create command) — Set: `id, err := svc.SetSandbox(ctx, sandbox)` → `resp, err := svc.SetSandbox(ctx, connect.NewRequest(sandbox))` + `resp.Msg.Id`

Update command — Read-Modify-Write (note: `existing` becomes `existingResp.Msg`):
```go
// Before
existing, err := svc.GetSandbox(context.Background(), &commonv1.Id{Id: args[0]})
if err != nil { return err }
if cmd.Flags().Changed("name") {
    existing.Name = name
}
id, err := svc.SetSandbox(context.Background(), existing)
return output.Print(gf.Pretty, map[string]string{"sandbox_id": id.Id})

// After
existingResp, err := svc.GetSandbox(context.Background(), connect.NewRequest(&commonv1.Id{Id: args[0]}))
if err != nil { return err }
if cmd.Flags().Changed("name") {
    existingResp.Msg.Name = name
}
resp, err := svc.SetSandbox(context.Background(), connect.NewRequest(existingResp.Msg))
return output.Print(gf.Pretty, map[string]string{"sandbox_id": resp.Msg.Id})
```

`StopSandbox` — Discard: wrap request
`DeleteSandbox` — Discard: wrap request

- [ ] **Step 3: Update session call sites**

`GetSessions` — List: `resp.Sessions` → `resp.Msg.Sessions`

`GetSession` — Get single: `session, err := ...` → `resp, err := ...` + `output.Print(gf.Pretty, resp.Msg)`

`NewSession` — Set: `id, err := svc.NewSession(ctx, req)` → `resp, err := svc.NewSession(ctx, connect.NewRequest(req))` + `resp.Msg.Id`

`SetPartialSession` — Set: `id, err := svc.SetPartialSession(ctx, &commonv1.PartialData{...})` → `resp, err := svc.SetPartialSession(ctx, connect.NewRequest(&commonv1.PartialData{...}))` + `resp.Msg.Id`

`StopSession` — Discard: wrap request
`DeleteSession` — Discard: wrap request

- [ ] **Step 4: Build and commit**

```bash
go build ./internal/cmd/sandbox/...
git add internal/cmd/sandbox/command.go
git commit -m "feat: migrate sandbox commands to Connect client"
```

---

## Task 15 — Migrate `internal/cmd/task/command.go`

**Files:**
- Modify: `internal/cmd/task/command.go`

- [ ] **Step 1: Add import, replace `connect()` function**

```go
func connect(gf *flags.Global) (taskv1.TaskServiceClient, func(), error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return nil, nil, err
	}
	httpClient := client.New(jwt, gf.Insecure)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return taskv1.NewTaskServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}
```

- [ ] **Step 2: Update all call sites**

`GetTasks` — List:
```go
// Before
resp, err := svc.GetTasks(context.Background(), &taskv1.TasksRequest{Filter: filter})
return output.Print(gf.Pretty, resp.Tasks)
// After
resp, err := svc.GetTasks(context.Background(), connect.NewRequest(&taskv1.TasksRequest{Filter: filter}))
return output.Print(gf.Pretty, resp.Msg.Tasks)
```

`GetTask` — Get single:
```go
// Before
task, err := svc.GetTask(context.Background(), &commonv1.Id{Id: args[0]})
return output.Print(gf.Pretty, task)
// After
resp, err := svc.GetTask(context.Background(), connect.NewRequest(&commonv1.Id{Id: args[0]}))
return output.Print(gf.Pretty, resp.Msg)
```

`GetTaskByKey` — Get single:
```go
// Before
task, err := svc.GetTaskByKey(context.Background(), &taskv1.TaskByKeyRequest{...})
return output.Print(gf.Pretty, task)
// After
resp, err := svc.GetTaskByKey(context.Background(), connect.NewRequest(&taskv1.TaskByKeyRequest{...}))
return output.Print(gf.Pretty, resp.Msg)
```

`SetTask` (create) — Set:
```go
// Before
id, err := svc.SetTask(context.Background(), task)
return output.Print(gf.Pretty, map[string]string{"task_id": id.Id})
// After
resp, err := svc.SetTask(context.Background(), connect.NewRequest(task))
return output.Print(gf.Pretty, map[string]string{"task_id": resp.Msg.Id})
```

`SetPartialTask` (update) — Set:
```go
// Before
id, err := svc.SetPartialTask(context.Background(), &commonv1.PartialData{Id: args[0], Data: data})
return output.Print(gf.Pretty, map[string]string{"task_id": id.Id})
// After
resp, err := svc.SetPartialTask(context.Background(), connect.NewRequest(&commonv1.PartialData{Id: args[0], Data: data}))
return output.Print(gf.Pretty, map[string]string{"task_id": resp.Msg.Id})
```

`DeleteTask` — Discard:
```go
// Before
_, err = svc.DeleteTask(context.Background(), &commonv1.Id{Id: args[0]})
// After
_, err = svc.DeleteTask(context.Background(), connect.NewRequest(&commonv1.Id{Id: args[0]}))
```

`AddTaskAttachment` — Get single (returns full task):
```go
// Before
task, err := svc.AddTaskAttachment(context.Background(), &taskv1.AddTaskAttachmentRequest{...})
return output.Print(gf.Pretty, task)
// After
resp, err := svc.AddTaskAttachment(context.Background(), connect.NewRequest(&taskv1.AddTaskAttachmentRequest{...}))
return output.Print(gf.Pretty, resp.Msg)
```

`DeleteTaskAttachment` — Get single (returns full task):
```go
// Before
task, err := svc.DeleteTaskAttachment(context.Background(), &taskv1.DeleteTaskAttachmentRequest{...})
return output.Print(gf.Pretty, task)
// After
resp, err := svc.DeleteTaskAttachment(context.Background(), connect.NewRequest(&taskv1.DeleteTaskAttachmentRequest{...}))
return output.Print(gf.Pretty, resp.Msg)
```

- [ ] **Step 3: Build and commit**

```bash
go build ./internal/cmd/task/...
git add internal/cmd/task/command.go
git commit -m "feat: migrate task commands to Connect client"
```

---

## Task 16 — Final cleanup and verification

**Files:** none new

- [ ] **Step 1: Full build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 2: go mod tidy**

```bash
go mod tidy
```

This removes `google.golang.org/grpc` as a direct dependency (it becomes transitive via connectrpc) and `github.com/grpc-ecosystem/grpc-gateway/v2` (no longer referenced). Review the diff — if anything looks unexpected, investigate before committing.

- [ ] **Step 3: Full test suite**

```bash
go test ./...
```

Expected: all tests pass including the new `internal/client/connect_test.go`.

- [ ] **Step 4: Smoke test — gRPC (default)**

```bash
go build -o /tmp/retask ./cmd/retask/
/tmp/retask task list --pretty
```

Expected: list of tasks printed. No `NWEB_API_TRANSPORT` set = gRPC protocol.

- [ ] **Step 5: Smoke test — HTTP transport**

```bash
NWEB_API_TRANSPORT=http /tmp/retask task list --pretty
```

Expected: same list of tasks. Connect HTTP protocol used.

- [ ] **Step 6: Final commit**

```bash
git add go.mod go.sum
git commit -m "chore: go mod tidy after Connect migration — remove grpc-gateway direct dep"
```
