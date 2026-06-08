# Retask CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a public Go CLI (`retask`) for interacting with NWEB Retask APIs over gRPC, with JWT auth, multi-profile config, and AI-agent-friendly JSON output.

**Architecture:** Cobra CLI with one package per service (`internal/cmd/<service>/`), each exporting `NewCommand(*flags.Global) *cobra.Command`. Shared infrastructure in `internal/{auth,client,config,output,version}`. gRPC over TLS using OS cert pool; PAT→JWT exchange cached per profile in `~/.config/retask/config.yaml`.

**Tech Stack:** Go 1.23, `github.com/spf13/cobra`, `google.golang.org/grpc`, `gopkg.in/yaml.v3`, `github.com/stretchr/testify`

---

## File Map

```
cmd/retask/main.go
internal/flags/flags.go
internal/version/version.go
internal/output/output.go
internal/output/output_test.go
internal/config/profile.go
internal/config/profile_test.go
internal/auth/token.go
internal/auth/token_test.go
internal/client/grpc.go
internal/cmd/auth/command.go
internal/cmd/workspace/command.go
internal/cmd/customer/command.go
internal/cmd/project/command.go
internal/cmd/file/command.go
internal/cmd/integration/command.go
internal/cmd/task/command.go
internal/cmd/projectconfig/command.go
internal/cmd/sandbox/command.go
internal/cmd/agent/command.go
internal/cmd/helpcmd/command.go
skills/retask-cli.md
proto/                          (approved protos only)
proto-gen/                      (generated Go — replaces api-contracts-gen/)
.bin/sync_proto.sh
.bin/build_proto.sh             (updated)
buf.yaml                        (updated)
buf.gen.yaml                    (updated)
go.mod                          (updated)
```

---

## Task 1: Proto Migration — Scrub History & Populate proto/

**Files:**
- Create: `proto/` directory tree
- Create: `.bin/sync_proto.sh`
- Modify: `.gitignore`

- [ ] **Step 1: Install git-filter-repo if needed**

```bash
pip install git-filter-repo
# verify:
git filter-repo --version
```

- [ ] **Step 2: Scrub api-contracts from git history**

```bash
# From repo root. This rewrites all commits — api-contracts/ disappears from history.
git filter-repo --path api-contracts --invert-paths
```

Expected: git log shows no api-contracts files in any commit.

- [ ] **Step 3: Verify api-contracts is gitignored**

```bash
grep "api-contracts" .gitignore
```

Expected: line `api-contracts` is present. If not, add it.

- [ ] **Step 4: Create sync_proto.sh**

```bash
cat > .bin/sync_proto.sh << 'EOF'
#!/bin/bash
# Copies approved proto files from local api-contracts/ into proto/
# Run from repo root. Requires api-contracts/ to be present locally (gitignored).
set -e

if [ ! -d "api-contracts" ]; then
  echo "ERROR: api-contracts/ not found. Clone it locally first (it is gitignored)."
  exit 1
fi

APPROVED=(
  "auth/v1"
  "common/v1"
  "customer/v1"
  "file/v1"
  "integration/v1"
  "project/v1"
  "retask/common/v1"
  "retask/agent/v1"
  "retask/project/v1"
  "retask/sandbox/v1"
  "retask/task/v1"
  "workspace/v1"
)
# Add new approved services here ↑

for svc in "${APPROVED[@]}"; do
  mkdir -p "proto/${svc}"
  cp api-contracts/proto/${svc}/*.proto proto/${svc}/
done

echo "Synced ${#APPROVED[@]} services. Run .bin/build_proto.sh to regenerate."
EOF
chmod +x .bin/sync_proto.sh
```

- [ ] **Step 5: Run sync to populate proto/**

```bash
./.bin/sync_proto.sh
```

Expected: `proto/` tree populated with `.proto` files for all 12 approved services.

- [ ] **Step 6: Verify proto/ contents**

```bash
find proto -name "*.proto" | sort
```

Expected: files for auth, common, customer, file, integration, project, retask/agent, retask/common, retask/project, retask/sandbox, retask/task, workspace — no event/, command/, cron/ subdirs.

- [ ] **Step 7: Commit**

```bash
git add proto/ .bin/sync_proto.sh
git commit -m "feat: add approved proto sources, sync_proto.sh"
```

---

## Task 2: Update Build Configuration

**Files:**
- Modify: `buf.yaml`
- Modify: `buf.gen.yaml`
- Modify: `.bin/build_proto.sh`

- [ ] **Step 1: Update buf.yaml**

```yaml
# buf.yaml
version: v2
modules:
  - path: proto
```

- [ ] **Step 2: Update buf.gen.yaml**

```yaml
# buf.gen.yaml
version: v2
managed:
  enabled: true
  disable:
    - file_option: go_package
      module: buf.build/googleapis/googleapis
  override:
    - file_option: go_package_prefix
      value: nweb.xyz/retask-cli/proto-gen
plugins:
  - remote: buf.build/protocolbuffers/go:v1.30.0
    out: proto-gen
    opt: paths=source_relative
  - remote: buf.build/grpc/go:v1.3.0
    out: proto-gen
    opt: paths=source_relative
  - remote: buf.build/grpc-ecosystem/gateway:v2.16.0
    out: proto-gen
    opt: paths=source_relative
```

- [ ] **Step 3: Update build_proto.sh**

```bash
cat > .bin/build_proto.sh << 'EOF'
#!/bin/bash
set -e

APPROVED_SERVICES=(
  "auth/v1"
  "common/v1"
  "customer/v1"
  "file/v1"
  "integration/v1"
  "project/v1"
  "retask/agent/v1"
  "retask/common/v1"
  "retask/project/v1"
  "retask/sandbox/v1"
  "retask/task/v1"
  "workspace/v1"
)
# Add new approved services here ↑

echo "=== Run: buf generate ."
buf generate .

echo "=== Run: protoc-go-inject-tag"
for svc in "${APPROVED_SERVICES[@]}"; do
  for f in proto-gen/${svc}/*.pb.go; do
    [ -f "$f" ] || continue
    echo "- ${f}"
    protoc-go-inject-tag -input="${f}"
  done
done

echo "=== Completed."
EOF
chmod +x .bin/build_proto.sh
```

- [ ] **Step 4: Delete old api-contracts-gen/ and regenerate**

```bash
rm -rf api-contracts-gen/
./.bin/build_proto.sh
```

Expected: `proto-gen/` directory created with Go files for all approved services.

- [ ] **Step 5: Verify proto-gen/ has only approved services**

```bash
ls proto-gen/
# Should show: auth  common  customer  file  integration  project  retask  workspace
# Should NOT show: ai  cron  payment  quota  migration  message  secret  etc.
```

- [ ] **Step 6: Commit**

```bash
git add buf.yaml buf.gen.yaml .bin/build_proto.sh proto-gen/
git commit -m "feat: migrate to proto-gen/, update buf config"
```

---

## Task 3: Update go.mod and Install Dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Update go.mod**

```
module nweb.xyz/retask-cli

go 1.23.4

require (
    github.com/spf13/cobra v1.8.0
    github.com/stretchr/testify v1.9.0
    google.golang.org/grpc v1.63.0
    google.golang.org/protobuf v1.34.0
    gopkg.in/yaml.v3 v3.0.1
)
```

- [ ] **Step 2: Tidy dependencies**

```bash
go mod tidy
```

Expected: `go.sum` created/updated. No errors.

- [ ] **Step 3: Verify proto-gen packages import correctly**

```bash
go build ./proto-gen/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "feat: add CLI dependencies to go.mod"
```

---

## Task 4: Core — version, flags, output packages

**Files:**
- Create: `internal/version/version.go`
- Create: `internal/flags/flags.go`
- Create: `internal/output/output.go`
- Create: `internal/output/output_test.go`

- [ ] **Step 1: Write version.go**

```go
// internal/version/version.go
package version

// Version is set at build time via:
// go build -ldflags "-X nweb.xyz/retask-cli/internal/version.Version=0.1.0"
var Version = "dev"
```

- [ ] **Step 2: Write flags.go**

```go
// internal/flags/flags.go
package flags

// Global holds persistent flags available on every command.
type Global struct {
    Profile     string
    WorkspaceID string
    Pretty      bool
    Insecure    bool
    NoSave      bool
    ConfigPath  string
}
```

- [ ] **Step 3: Write failing test for output package**

```go
// internal/output/output_test.go
package output_test

import (
    "bytes"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "nweb.xyz/retask-cli/internal/output"
)

func TestPrintJSON(t *testing.T) {
    var buf bytes.Buffer
    err := output.Fprint(&buf, false, map[string]string{"key": "value"})
    require.NoError(t, err)
    assert.Contains(t, buf.String(), `"key": "value"`)
}

func TestPrintPretty(t *testing.T) {
    var buf bytes.Buffer
    err := output.Fprint(&buf, true, []map[string]any{
        {"id": "abc", "name": "Test"},
    })
    require.NoError(t, err)
    assert.Contains(t, buf.String(), "abc")
    assert.Contains(t, buf.String(), "Test")
}
```

- [ ] **Step 4: Run test to verify it fails**

```bash
go test ./internal/output/... 2>&1 | head -5
```

Expected: `cannot find package` or `undefined: output.Fprint`

- [ ] **Step 5: Implement output.go**

```go
// internal/output/output.go
package output

import (
    "encoding/json"
    "fmt"
    "io"
    "os"
    "reflect"
    "strings"
    "text/tabwriter"
)

// Fprint writes v as JSON (pretty=false) or a human table (pretty=true).
func Fprint(w io.Writer, pretty bool, v any) error {
    if pretty {
        return fprintTable(w, v)
    }
    enc := json.NewEncoder(w)
    enc.SetIndent("", "  ")
    return enc.Encode(v)
}

// Print writes to os.Stdout.
func Print(pretty bool, v any) error {
    return Fprint(os.Stdout, pretty, v)
}

// Err writes an error message to stderr.
func Err(format string, args ...any) {
    fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
}

func fprintTable(w io.Writer, v any) error {
    // Reflect to get slice of maps or structs; fall back to JSON for non-slice.
    rv := reflect.ValueOf(v)
    if rv.Kind() == reflect.Ptr {
        rv = rv.Elem()
    }
    if rv.Kind() != reflect.Slice {
        // Single value: print as key: value pairs
        data, err := json.MarshalIndent(v, "", "  ")
        if err != nil {
            return err
        }
        var m map[string]any
        if err := json.Unmarshal(data, &m); err != nil {
            _, werr := fmt.Fprintln(w, string(data))
            return werr
        }
        tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
        for k, val := range m {
            fmt.Fprintf(tw, "%s\t%v\n", k, val)
        }
        return tw.Flush()
    }

    // Slice: print as table with header row from JSON keys
    data, err := json.Marshal(v)
    if err != nil {
        return err
    }
    var rows []map[string]any
    if err := json.Unmarshal(data, &rows); err != nil || len(rows) == 0 {
        _, werr := fmt.Fprintln(w, string(data))
        return werr
    }

    // Collect headers from first row
    headers := make([]string, 0)
    for k := range rows[0] {
        headers = append(headers, k)
    }

    tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
    fmt.Fprintln(tw, strings.Join(headers, "\t"))
    for _, row := range rows {
        vals := make([]string, len(headers))
        for i, h := range headers {
            vals[i] = fmt.Sprintf("%v", row[h])
        }
        fmt.Fprintln(tw, strings.Join(vals, "\t"))
    }
    return tw.Flush()
}
```

- [ ] **Step 6: Run test to verify it passes**

```bash
go test ./internal/output/... -v
```

Expected: `PASS`

- [ ] **Step 7: Commit**

```bash
git add internal/version/ internal/flags/ internal/output/
git commit -m "feat: add version, flags, output packages"
```

---

## Task 5: Core — config package

**Files:**
- Create: `internal/config/profile.go`
- Create: `internal/config/profile_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/config/profile_test.go
package config_test

import (
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "nweb.xyz/retask-cli/internal/config"
)

func TestLoadMissing(t *testing.T) {
    cfg, err := config.Load("/tmp/retask-test-nonexistent-abc123.yaml")
    require.NoError(t, err)
    p := cfg.ActiveProfileData("")
    assert.Equal(t, "api.dev.nweb.app:443", p.Endpoint)
}

func TestSaveAndLoad(t *testing.T) {
    path := filepath.Join(t.TempDir(), "config.yaml")
    exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

    cfg := &config.Config{
        ActiveProfile: "default",
        Profiles: map[string]config.Profile{
            "default": {
                Endpoint:     "api.dev.nweb.app:443",
                WorkspaceID:  "ws_abc",
                CachedJWT:    "tok123",
                JWTExpiresAt: exp,
            },
        },
    }
    require.NoError(t, cfg.Save(path))

    loaded, err := config.Load(path)
    require.NoError(t, err)
    p := loaded.ActiveProfileData("default")
    assert.Equal(t, "ws_abc", p.WorkspaceID)
    assert.Equal(t, "tok123", p.CachedJWT)
    assert.Equal(t, exp, p.JWTExpiresAt)
}

func TestFilePermissions(t *testing.T) {
    path := filepath.Join(t.TempDir(), "config.yaml")
    cfg := &config.Config{ActiveProfile: "default", Profiles: map[string]config.Profile{}}
    require.NoError(t, cfg.Save(path))
    info, err := os.Stat(path)
    require.NoError(t, err)
    assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... 2>&1 | head -5
```

Expected: `cannot find package`

- [ ] **Step 3: Implement profile.go**

```go
// internal/config/profile.go
package config

import (
    "os"
    "path/filepath"
    "time"

    "gopkg.in/yaml.v3"
)

const DefaultEndpoint = "api.dev.nweb.app:443"

type Profile struct {
    Endpoint     string    `yaml:"endpoint"`
    WorkspaceID  string    `yaml:"workspace_id,omitempty"`
    CachedJWT    string    `yaml:"cached_jwt,omitempty"`
    JWTExpiresAt time.Time `yaml:"jwt_expires_at,omitempty"`
}

type Config struct {
    ActiveProfile string             `yaml:"active_profile"`
    Profiles      map[string]Profile `yaml:"profiles"`
}

func DefaultConfigPath() string {
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".config", "retask", "config.yaml")
}

func Load(path string) (*Config, error) {
    data, err := os.ReadFile(path)
    if os.IsNotExist(err) {
        return &Config{
            ActiveProfile: "default",
            Profiles: map[string]Profile{
                "default": {Endpoint: DefaultEndpoint},
            },
        }, nil
    }
    if err != nil {
        return nil, err
    }
    var cfg Config
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }
    if cfg.Profiles == nil {
        cfg.Profiles = map[string]Profile{}
    }
    return &cfg, nil
}

func (c *Config) Save(path string) error {
    if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
        return err
    }
    data, err := yaml.Marshal(c)
    if err != nil {
        return err
    }
    return os.WriteFile(path, data, 0600)
}

// ActiveProfileData returns the profile for the given name (or c.ActiveProfile if empty).
func (c *Config) ActiveProfileData(name string) Profile {
    if name == "" {
        name = c.ActiveProfile
    }
    if name == "" {
        name = "default"
    }
    p, ok := c.Profiles[name]
    if !ok {
        p = Profile{Endpoint: DefaultEndpoint}
    }
    if p.Endpoint == "" {
        p.Endpoint = DefaultEndpoint
    }
    return p
}

// SetProfile upserts a profile.
func (c *Config) SetProfile(name string, p Profile) {
    if c.Profiles == nil {
        c.Profiles = map[string]Profile{}
    }
    c.Profiles[name] = p
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/... -v
```

Expected: `PASS` for all three tests.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: add config/profile package"
```

---

## Task 6: Core — auth/token package

**Files:**
- Create: `internal/auth/token.go`
- Create: `internal/auth/token_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/auth/token_test.go
package auth_test

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "nweb.xyz/retask-cli/internal/auth"
    "nweb.xyz/retask-cli/internal/config"
)

func TestDirectToken(t *testing.T) {
    t.Setenv("NWEB_API_TOKEN", "direct-jwt")
    r := &auth.Resolver{Profile: config.Profile{Endpoint: "localhost:443"}}
    tok, err := r.Token(context.Background())
    require.NoError(t, err)
    assert.Equal(t, "direct-jwt", tok)
}

func TestCachedJWTValid(t *testing.T) {
    t.Setenv("NWEB_API_TOKEN", "")
    r := &auth.Resolver{
        Profile: config.Profile{
            CachedJWT:    "cached-jwt",
            JWTExpiresAt: time.Now().Add(10 * time.Minute),
        },
    }
    tok, err := r.Token(context.Background())
    require.NoError(t, err)
    assert.Equal(t, "cached-jwt", tok)
}

func TestCachedJWTExpired(t *testing.T) {
    t.Setenv("NWEB_API_TOKEN", "")
    t.Setenv("NWEB_API_KEY", "") // no PAT → should error
    r := &auth.Resolver{
        Profile: config.Profile{
            CachedJWT:    "expired-jwt",
            JWTExpiresAt: time.Now().Add(-1 * time.Minute),
        },
    }
    _, err := r.Token(context.Background())
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "NWEB_API_KEY")
}

func TestMissingPAT(t *testing.T) {
    t.Setenv("NWEB_API_TOKEN", "")
    t.Setenv("NWEB_API_KEY", "")
    r := &auth.Resolver{Profile: config.Profile{}}
    _, err := r.Token(context.Background())
    require.Error(t, err)
    assert.Contains(t, err.Error(), "NWEB_API_KEY")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/auth/... 2>&1 | head -5
```

Expected: `cannot find package`

- [ ] **Step 3: Implement token.go**

```go
// internal/auth/token.go
package auth

import (
    "bufio"
    "context"
    "fmt"
    "os"
    "strings"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials"
    "google.golang.org/grpc/credentials/insecure"
    authv1 "nweb.xyz/retask-cli/proto-gen/auth/v1"
    commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
    "nweb.xyz/retask-cli/internal/config"
)

// Resolver resolves a valid JWT for API calls.
type Resolver struct {
    Profile     config.Profile
    ProfileName string
    WorkspaceID string // flag override
    NoPersist   bool
    ConfigPath  string
    Insecure    bool

    // OnTokenRefreshed is called after a successful PAT exchange.
    // Used by commands to persist the new JWT to the profile.
    OnTokenRefreshed func(jwt string, expiresAt time.Time, workspaceID string)
}

// Token returns a valid JWT, exchanging the PAT if necessary.
// PAT is never stored; it is always read fresh from NWEB_API_KEY.
func (r *Resolver) Token(ctx context.Context) (string, error) {
    // 1. Ready-to-use JWT from env
    if tok := os.Getenv("NWEB_API_TOKEN"); tok != "" {
        return tok, nil
    }

    // 2. Cached JWT still valid (more than 5 min remaining)
    if r.Profile.CachedJWT != "" && time.Now().Add(5*time.Minute).Before(r.Profile.JWTExpiresAt) {
        return r.Profile.CachedJWT, nil
    }

    // 3. Need PAT to exchange
    pat := os.Getenv("NWEB_API_KEY")
    if pat == "" {
        return "", fmt.Errorf("NWEB_API_KEY is required (set your PAT). Run: retask auth login")
    }

    // 4. Resolve workspace_id
    wsID, err := r.resolveWorkspaceID()
    if err != nil {
        return "", err
    }

    // 5. Exchange PAT for JWT
    jwt, expiresAt, err := r.exchangePAT(ctx, pat, wsID)
    if err != nil {
        return "", fmt.Errorf("PAT exchange failed: %w", err)
    }

    // 6. Persist (unless --no-save)
    if r.OnTokenRefreshed != nil {
        r.OnTokenRefreshed(jwt, expiresAt, wsID)
    }

    return jwt, nil
}

func (r *Resolver) resolveWorkspaceID() (string, error) {
    // Priority: flag > env > profile > prompt (TTY only)
    if r.WorkspaceID != "" {
        return r.WorkspaceID, nil
    }
    if v := os.Getenv("NWEB_WORKSPACE_ID"); v != "" {
        return v, nil
    }
    if r.Profile.WorkspaceID != "" {
        return r.Profile.WorkspaceID, nil
    }

    // Interactive prompt only on TTY
    fi, err := os.Stdin.Stat()
    if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
        return "", fmt.Errorf(
            "workspace ID required. Set NWEB_WORKSPACE_ID, use --workspace-id, or run: retask auth login",
        )
    }

    fmt.Fprint(os.Stderr, "Enter workspace ID: ")
    scanner := bufio.NewScanner(os.Stdin)
    if scanner.Scan() {
        id := strings.TrimSpace(scanner.Text())
        if id != "" {
            return id, nil
        }
    }
    return "", fmt.Errorf("workspace ID is required")
}

func (r *Resolver) exchangePAT(ctx context.Context, pat, workspaceID string) (string, time.Time, error) {
    var opts []grpc.DialOption
    if r.Insecure {
        opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
    } else {
        opts = append(opts, grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, "")))
    }

    conn, err := grpc.NewClient(r.Profile.Endpoint, opts...)
    if err != nil {
        return "", time.Time{}, err
    }
    defer conn.Close()

    client := authv1.NewAuthServiceClient(conn)
    resp, err := client.ExchangePat(ctx, &authv1.PatExchangeRequest{
        Token:       pat,
        WorkspaceId: workspaceID,
    })
    if err != nil {
        return "", time.Time{}, err
    }

    var expiresAt time.Time
    if resp.ExpiresAt != nil {
        expiresAt = resp.ExpiresAt.AsTime()
    }
    return resp.Jwt, expiresAt, nil
}

// ExportEnv returns shell export lines for --no-save mode.
func ExportEnv(jwt, workspaceID string) string {
    return fmt.Sprintf(
        "export NWEB_API_TOKEN=%q\nexport NWEB_WORKSPACE_ID=%q\n# To apply: eval $(retask auth login --no-save)\n",
        jwt, workspaceID,
    )
}

// NewResolver builds a Resolver from global flags and a loaded profile.
// configPath is used to persist the refreshed token unless noPersist is true.
func NewResolver(profile config.Profile, profileName, workspaceIDFlag, configPath string, noPersist, insecureFlag bool) *Resolver {
    r := &Resolver{
        Profile:     profile,
        ProfileName: profileName,
        WorkspaceID: workspaceIDFlag,
        NoPersist:   noPersist,
        ConfigPath:  configPath,
        Insecure:    insecureFlag,
    }
    if !noPersist {
        r.OnTokenRefreshed = func(jwt string, expiresAt time.Time, wsID string) {
            cfg, err := config.Load(configPath)
            if err != nil {
                return
            }
            p := cfg.ActiveProfileData(profileName)
            p.CachedJWT = jwt
            p.JWTExpiresAt = expiresAt
            if wsID != "" {
                p.WorkspaceID = wsID
            }
            cfg.SetProfile(profileName, p)
            _ = cfg.Save(configPath)
        }
    }
    return r
}

// PAT never passed here — only used in ExchangePat above.
// This function adds JWT to outgoing gRPC call metadata.
func BearerToken(jwt string) string {
    return "Bearer " + jwt
}

// GetPATOnce returns NWEB_API_KEY without storing it anywhere.
// Only for auth commands that explicitly manage PATs.
func GetPATOnce() string {
    return os.Getenv("NWEB_API_KEY")
}

// EmptyProto returns a commonv1.Empty message (convenience).
func EmptyProto() *commonv1.Empty {
    return &commonv1.Empty{}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/auth/... -v
```

Expected: `TestDirectToken PASS`, `TestCachedJWTValid PASS`, `TestCachedJWTExpired PASS`, `TestMissingPAT PASS`

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "feat: add auth/token resolver"
```

---

## Task 7: Core — gRPC Client

**Files:**
- Create: `internal/client/grpc.go`

- [ ] **Step 1: Write grpc.go**

```go
// internal/client/grpc.go
package client

import (
    "context"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials"
    "google.golang.org/grpc/credentials/insecure"
    "google.golang.org/grpc/metadata"
)

// New creates a gRPC client connection with TLS and JWT auth interceptor.
// Uses OS cert pool — works on macOS, Linux, Windows without bundled certs.
// Pass insecureFlag=true only for local dev against localhost.
func New(endpoint, jwt string, insecureFlag bool) (*grpc.ClientConn, error) {
    var creds grpc.DialOption
    if insecureFlag {
        creds = grpc.WithTransportCredentials(insecure.NewCredentials())
    } else {
        creds = grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(nil, ""))
    }
    return grpc.NewClient(
        endpoint,
        creds,
        grpc.WithUnaryInterceptor(authInterceptor(jwt)),
    )
}

func authInterceptor(jwt string) grpc.UnaryClientInterceptor {
    return func(ctx context.Context, method string, req, reply any,
        cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
        if jwt != "" {
            ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+jwt)
        }
        return invoker(ctx, method, req, reply, cc, opts...)
    }
}
```

- [ ] **Step 2: Build to verify**

```bash
go build ./internal/client/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/client/
git commit -m "feat: add gRPC client factory"
```

---

## Task 8: Root Command & main.go

**Files:**
- Create: `cmd/retask/main.go`

- [ ] **Step 1: Create cmd/retask/main.go**

```go
// cmd/retask/main.go
package main

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/version"
)

func main() {
    if err := newRootCommand().Execute(); err != nil {
        os.Exit(1)
    }
}

func newRootCommand() *cobra.Command {
    gf := &flags.Global{}

    root := &cobra.Command{
        Use:          "retask",
        Short:        "Retask CLI — interact with NWEB Retask APIs",
        SilenceUsage: true,
        Version:      version.Version,
    }

    root.PersistentFlags().StringVar(&gf.Profile, "profile", "", "Config profile name (env: RETASK_PROFILE)")
    root.PersistentFlags().StringVar(&gf.WorkspaceID, "workspace-id", "", "Workspace ID (env: NWEB_WORKSPACE_ID)")
    root.PersistentFlags().BoolVar(&gf.Pretty, "pretty", false, "Human-readable table output (default: JSON)")
    root.PersistentFlags().BoolVar(&gf.Insecure, "insecure", false, "Skip TLS verification (local dev only)")
    root.PersistentFlags().BoolVar(&gf.NoSave, "no-save", false, "Don't write credentials to config file")
    root.PersistentFlags().StringVar(&gf.ConfigPath, "config", "", "Config file path (default: ~/.config/retask/config.yaml)")

    root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
        // Apply env overrides for flags not explicitly set
        if gf.Profile == "" {
            gf.Profile = os.Getenv("RETASK_PROFILE")
        }
        if gf.WorkspaceID == "" {
            gf.WorkspaceID = os.Getenv("NWEB_WORKSPACE_ID")
        }
        if os.Getenv("RETASK_NO_PERSIST") != "" {
            gf.NoSave = true
        }
        return nil
    }

    // Version flag output
    root.SetVersionTemplate(fmt.Sprintf("retask version %s\n", version.Version))

    // Service commands registered here — add one line per new service
    // (populated in subsequent tasks)

    return root
}
```

- [ ] **Step 2: Build to verify**

```bash
go build ./cmd/retask/
```

Expected: no errors.

- [ ] **Step 3: Smoke test**

```bash
./retask --version
./retask --help
```

Expected: version prints `retask version dev`, help shows global flags.

- [ ] **Step 4: Commit**

```bash
git add cmd/
git commit -m "feat: add root cobra command"
```

---

## Task 9: auth commands

**Files:**
- Create: `internal/cmd/auth/command.go`
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Write auth/command.go**

```go
// internal/cmd/auth/command.go
package auth

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "time"

    "github.com/spf13/cobra"
    "google.golang.org/protobuf/types/known/timestamppb"
    "nweb.xyz/retask-cli/internal/auth"
    "nweb.xyz/retask-cli/internal/client"
    "nweb.xyz/retask-cli/internal/config"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/output"
    authv1 "nweb.xyz/retask-cli/proto-gen/auth/v1"
    commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
)

func NewCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "auth",
        Short: "Manage authentication, tokens, and PATs",
    }
    cmd.AddCommand(
        newLoginCommand(gf),
        newLogoutCommand(gf),
        newWhoamiCommand(gf),
        newPatCommand(gf),
    )
    return cmd
}

// helpers shared across auth sub-commands

func loadProfile(gf *flags.Global) (config.Profile, string, error) {
    path := gf.ConfigPath
    if path == "" {
        path = config.DefaultConfigPath()
    }
    cfg, err := config.Load(path)
    if err != nil {
        return config.Profile{}, path, err
    }
    return cfg.ActiveProfileData(gf.Profile), path, nil
}

func buildResolver(gf *flags.Global) (*auth.Resolver, error) {
    profile, cfgPath, err := loadProfile(gf)
    if err != nil {
        return nil, err
    }
    return auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, cfgPath, gf.NoSave, gf.Insecure), nil
}

// login

func newLoginCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "login",
        Short: "Exchange PAT for JWT and save to profile",
        Long: `Exchange a Personal Access Token (NWEB_API_KEY) for a JWT and save it to the active profile.

Usage example:
  retask auth login
  eval $(retask auth login --no-save)   # shared sandbox: session-scoped credentials

Environment:
  NWEB_API_KEY        Required. PAT starting with "nweb_pat_..."
  NWEB_WORKSPACE_ID   Required if not in profile or --workspace-id not set`,
        RunE: func(cmd *cobra.Command, args []string) error {
            resolver, err := buildResolver(gf)
            if err != nil {
                return err
            }
            jwt, err := resolver.Token(context.Background())
            if err != nil {
                return err
            }
            if gf.NoSave {
                wsID := gf.WorkspaceID
                if wsID == "" {
                    wsID = os.Getenv("NWEB_WORKSPACE_ID")
                }
                fmt.Print(auth.ExportEnv(jwt, wsID))
                return nil
            }
            return output.Print(gf.Pretty, map[string]string{"status": "logged in"})
        },
    }
}

// logout

func newLogoutCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "logout",
        Short: "Clear cached JWT from active profile",
        RunE: func(cmd *cobra.Command, args []string) error {
            path := gf.ConfigPath
            if path == "" {
                path = config.DefaultConfigPath()
            }
            cfg, err := config.Load(path)
            if err != nil {
                return err
            }
            p := cfg.ActiveProfileData(gf.Profile)
            p.CachedJWT = ""
            p.JWTExpiresAt = time.Time{}
            name := gf.Profile
            if name == "" {
                name = cfg.ActiveProfile
            }
            cfg.SetProfile(name, p)
            if err := cfg.Save(path); err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "logged out"})
        },
    }
}

// whoami

func newWhoamiCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "whoami",
        Short: "Print current token claims (workspace, expiry)",
        RunE: func(cmd *cobra.Command, args []string) error {
            profile, _, err := loadProfile(gf)
            if err != nil {
                return err
            }
            if profile.CachedJWT == "" && os.Getenv("NWEB_API_TOKEN") == "" {
                return fmt.Errorf("not logged in. Run: retask auth login")
            }
            return output.Print(gf.Pretty, map[string]any{
                "workspace_id": profile.WorkspaceID,
                "jwt_expires":  profile.JWTExpiresAt.Format(time.RFC3339),
                "endpoint":     profile.Endpoint,
            })
        },
    }
}

// pat

func newPatCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "pat",
        Short: "Manage Personal Access Tokens",
    }
    cmd.AddCommand(newPatListCommand(gf), newPatCreateCommand(gf), newPatRevokeCommand(gf))
    return cmd
}

func newPatListCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "list",
        Short: "List PATs for current user",
        Long: `List Personal Access Tokens for the authenticated user.

Usage example:
  retask auth pat list

Output fields: pat_id, name, masked_value, scopes, expires_at, last_used_at`,
        RunE: func(cmd *cobra.Command, args []string) error {
            resolver, err := buildResolver(gf)
            if err != nil {
                return err
            }
            jwt, err := resolver.Token(context.Background())
            if err != nil {
                return err
            }
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
        },
    }
}

func newPatCreateCommand(gf *flags.Global) *cobra.Command {
    var name, description, expiresAt string
    cmd := &cobra.Command{
        Use:   "create",
        Short: "Create a new PAT",
        Long: `Create a new Personal Access Token.

Usage example:
  retask auth pat create --name "ci-bot" --description "CI pipeline token"
  retask auth pat create --name "temp" --expires-at 2026-12-31T00:00:00Z

Flags:
  --name string         Required. Display name for the PAT
  --description string  Optional description
  --expires-at string   Optional expiry in RFC3339 (e.g. 2026-12-31T00:00:00Z). Absent = no expiry`,
        RunE: func(cmd *cobra.Command, args []string) error {
            if name == "" {
                return fmt.Errorf("--name is required")
            }
            resolver, err := buildResolver(gf)
            if err != nil {
                return err
            }
            jwt, err := resolver.Token(context.Background())
            if err != nil {
                return err
            }
            profile, _, _ := loadProfile(gf)
            conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
            if err != nil {
                return err
            }
            defer conn.Close()

            req := &authv1.CreatePatRequest{
                Name:        name,
                Description: description,
                WorkspaceId: gf.WorkspaceID,
            }
            if expiresAt != "" {
                t, err := time.Parse(time.RFC3339, expiresAt)
                if err != nil {
                    return fmt.Errorf("--expires-at must be RFC3339 (e.g. 2026-12-31T00:00:00Z): %w", err)
                }
                req.ExpiresAt = timestamppb.New(t)
            }
            resp, err := authv1.NewAuthServiceClient(conn).CreatePat(context.Background(), req)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]any{
                "pat":       resp.Pat,
                "raw_token": resp.RawToken,
            })
        },
    }
    cmd.Flags().StringVar(&name, "name", "", "PAT display name (required)")
    cmd.Flags().StringVar(&description, "description", "", "PAT description")
    cmd.Flags().StringVar(&expiresAt, "expires-at", "", "Expiry in RFC3339 (absent = no expiry)")
    return cmd
}

func newPatRevokeCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "revoke <pat-id>",
        Short: "Revoke a PAT by ID",
        Long: `Revoke (soft-delete) a Personal Access Token.

Usage example:
  retask auth pat revoke pat_abc123`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            resolver, err := buildResolver(gf)
            if err != nil {
                return err
            }
            jwt, err := resolver.Token(context.Background())
            if err != nil {
                return err
            }
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
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "revoked", "pat_id": args[0]})
        },
    }
}

// ensure json import used
var _ = json.Marshal
```

- [ ] **Step 2: Register in main.go**

In `cmd/retask/main.go`, add import and `AddCommand`:

```go
import (
    // existing imports...
    authcmd "nweb.xyz/retask-cli/internal/cmd/auth"
)

// In newRootCommand(), before return root:
root.AddCommand(authcmd.NewCommand(gf))
```

- [ ] **Step 3: Build**

```bash
go build ./cmd/retask/
```

Expected: no errors.

- [ ] **Step 4: Smoke test**

```bash
./retask auth --help
./retask auth pat --help
```

Expected: help text shows login, logout, whoami, pat subcommands.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/auth/ cmd/retask/main.go
git commit -m "feat: add auth commands (login, logout, whoami, pat)"
```

---

## Task 10: workspace commands

**Files:**
- Create: `internal/cmd/workspace/command.go`
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Write workspace/command.go**

```go
// internal/cmd/workspace/command.go
package workspace

import (
    "context"
    "fmt"

    "github.com/spf13/cobra"
    "nweb.xyz/retask-cli/internal/auth"
    "nweb.xyz/retask-cli/internal/client"
    "nweb.xyz/retask-cli/internal/config"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/output"
    commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
    workspacev1 "nweb.xyz/retask-cli/proto-gen/workspace/v1"
)

func NewCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "workspace",
        Short: "Manage workspaces and members",
    }
    cmd.AddCommand(
        newListCommand(gf),
        newGetCommand(gf),
        newCreateCommand(gf),
        newUpdateCommand(gf),
        newDeleteCommand(gf),
        newMemberCommand(gf),
    )
    return cmd
}

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
    conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
    if err != nil {
        return nil, nil, err
    }
    return workspacev1.NewWorkspaceServiceClient(conn), func() { conn.Close() }, nil
}

func newListCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "list",
        Short: "List workspaces accessible to the authenticated user",
        Long: `List workspaces the authenticated user is a member of.

Usage example:
  retask workspace list

Output fields: workspace_id, name, description, color, created_at`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            resp, err := svc.GetWorkspaces(context.Background(), &workspacev1.WorkspacesRequest{})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Workspaces)
        },
    }
}

func newGetCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "get <workspace-id>",
        Short: "Get a workspace by ID",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            ws, err := svc.GetWorkspace(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, ws)
        },
    }
}

func newCreateCommand(gf *flags.Global) *cobra.Command {
    var name, description, color string
    cmd := &cobra.Command{
        Use:   "create",
        Short: "Create a new workspace",
        Long: `Create a new workspace.

Usage example:
  retask workspace create --name "My Team" --description "Engineering workspace" --color "#3B82F6"

Flags:
  --name string         Required. Workspace display name
  --description string  Optional description
  --color string        Optional hex color (e.g. #3B82F6)`,
        RunE: func(cmd *cobra.Command, args []string) error {
            if name == "" {
                return fmt.Errorf("--name is required")
            }
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            id, err := svc.SetWorkspace(context.Background(), &workspacev1.Workspace{
                Name:        name,
                Description: description,
                Color:       color,
            })
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"workspace_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&name, "name", "", "Workspace name (required)")
    cmd.Flags().StringVar(&description, "description", "", "Description")
    cmd.Flags().StringVar(&color, "color", "", "Hex color (e.g. #3B82F6)")
    return cmd
}

func newUpdateCommand(gf *flags.Global) *cobra.Command {
    var name, description, color string
    cmd := &cobra.Command{
        Use:   "update <workspace-id>",
        Short: "Update workspace fields",
        Long: `Update a workspace. Only provided flags are changed.

Usage example:
  retask workspace update ws_abc --name "New Name" --color "#EF4444"

Flags:
  --name string         New display name
  --description string  New description
  --color string        New hex color`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            // Fetch first to preserve existing fields
            ws, err := svc.GetWorkspace(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            if cmd.Flags().Changed("name") {
                ws.Name = name
            }
            if cmd.Flags().Changed("description") {
                ws.Description = description
            }
            if cmd.Flags().Changed("color") {
                ws.Color = color
            }
            id, err := svc.SetWorkspace(context.Background(), ws)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"workspace_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&name, "name", "", "New name")
    cmd.Flags().StringVar(&description, "description", "", "New description")
    cmd.Flags().StringVar(&color, "color", "", "New hex color")
    return cmd
}

func newDeleteCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "delete <workspace-id>",
        Short: "Delete a workspace",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.DeleteWorkspace(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "deleted", "workspace_id": args[0]})
        },
    }
}

func newMemberCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "member",
        Short: "Manage workspace members",
    }
    cmd.AddCommand(
        newMemberListCommand(gf),
        newMemberInviteCommand(gf),
        newMemberUpdateCommand(gf),
        newMemberRemoveCommand(gf),
    )
    return cmd
}

func newMemberListCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "list <workspace-id>",
        Short: "List members of a workspace",
        Long: `List workspace members.

Usage example:
  retask workspace member list ws_abc

Output fields: workspace_member_id, role, membership_status, display_name, invited_email`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            resp, err := svc.GetWorkspaceMembers(context.Background(), &workspacev1.WorkspaceMembersRequest{
                WorkspaceId: args[0],
            })
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Members)
        },
    }
}

func newMemberInviteCommand(gf *flags.Global) *cobra.Command {
    var email, displayName, role string
    cmd := &cobra.Command{
        Use:   "invite <workspace-id>",
        Short: "Invite a member to a workspace",
        Long: `Invite a user to a workspace by email.

Usage example:
  retask workspace member invite ws_abc --email user@example.com --role EDITOR --display-name "Alice"

Flags:
  --email string        Required. Email address to invite
  --role string         Member role. Values: VIEWER, EDITOR, ADMIN, OWNER (default: VIEWER)
  --display-name string Display name shown in the workspace`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            if email == "" {
                return fmt.Errorf("--email is required")
            }
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            r := workspacev1.WorkspaceMemberRole_VIEWER
            if role != "" {
                v, ok := workspacev1.WorkspaceMemberRole_value[role]
                if !ok {
                    return fmt.Errorf("invalid --role %q. Values: VIEWER, EDITOR, ADMIN, OWNER", role)
                }
                r = workspacev1.WorkspaceMemberRole(v)
            }
            _, err = svc.InviteWorkspaceMember(context.Background(), &workspacev1.InviteWorkspaceMemberRequest{
                WorkspaceId:   args[0],
                InvitedEmail:  email,
                DisplayName:   displayName,
                Role:          r,
            })
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "invited", "email": email})
        },
    }
    cmd.Flags().StringVar(&email, "email", "", "Email to invite (required)")
    cmd.Flags().StringVar(&displayName, "display-name", "", "Display name")
    cmd.Flags().StringVar(&role, "role", "", "Role: VIEWER, EDITOR, ADMIN, OWNER")
    return cmd
}

func newMemberUpdateCommand(gf *flags.Global) *cobra.Command {
    var role, displayName string
    cmd := &cobra.Command{
        Use:   "update <workspace-id> <member-id>",
        Short: "Update a workspace member's role or display name",
        Long: `Update a workspace member.

Usage example:
  retask workspace member update ws_abc mem_xyz --role ADMIN

Flags:
  --role string         New role. Values: VIEWER, EDITOR, ADMIN, OWNER
  --display-name string New display name`,
        Args: cobra.ExactArgs(2),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            req := &workspacev1.UpdateWorkspaceMemberRequest{
                WorkspaceId:       args[0],
                WorkspaceMemberId: args[1],
            }
            if cmd.Flags().Changed("role") {
                v, ok := workspacev1.WorkspaceMemberRole_value[role]
                if !ok {
                    return fmt.Errorf("invalid --role %q. Values: VIEWER, EDITOR, ADMIN, OWNER", role)
                }
                req.Role = workspacev1.WorkspaceMemberRole(v)
            }
            if cmd.Flags().Changed("display-name") {
                req.DisplayName = displayName
            }
            _, err = svc.UpdateWorkspaceMember(context.Background(), req)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "updated", "workspace_member_id": args[1]})
        },
    }
    cmd.Flags().StringVar(&role, "role", "", "New role: VIEWER, EDITOR, ADMIN, OWNER")
    cmd.Flags().StringVar(&displayName, "display-name", "", "New display name")
    return cmd
}

func newMemberRemoveCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "remove <workspace-id> <member-id>",
        Short: "Remove a member from a workspace",
        Args:  cobra.ExactArgs(2),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.RemoveWorkspaceMember(context.Background(), &workspacev1.RemoveWorkspaceMemberRequest{
                WorkspaceId:       args[0],
                WorkspaceMemberId: args[1],
            })
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "removed", "workspace_member_id": args[1]})
        },
    }
}
```

- [ ] **Step 2: Register in main.go**

```go
import workspacecmd "nweb.xyz/retask-cli/internal/cmd/workspace"
// root.AddCommand(workspacecmd.NewCommand(gf))
```

- [ ] **Step 3: Build and smoke test**

```bash
go build ./cmd/retask/ && ./retask workspace --help
```

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/workspace/ cmd/retask/main.go
git commit -m "feat: add workspace commands"
```

---

## Task 11: customer commands

**Files:**
- Create: `internal/cmd/customer/command.go`
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Write customer/command.go**

```go
// internal/cmd/customer/command.go
package customer

import (
    "context"
    "fmt"

    "github.com/spf13/cobra"
    "nweb.xyz/retask-cli/internal/auth"
    "nweb.xyz/retask-cli/internal/client"
    "nweb.xyz/retask-cli/internal/config"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/output"
    commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
    customerv1 "nweb.xyz/retask-cli/proto-gen/customer/v1"
)

func NewCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "customer",
        Short: "Manage customer profiles",
    }
    cmd.AddCommand(
        newProfileGetCommand(gf),
        newProfileSetCommand(gf),
        newListCommand(gf),
        newGetCommand(gf),
    )
    return cmd
}

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
    conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
    if err != nil {
        return nil, nil, err
    }
    return customerv1.NewCustomerServiceClient(conn), func() { conn.Close() }, nil
}

func newProfileGetCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "profile get",
        Short: "Get your profile",
        Long: `Get the authenticated user's customer profile.

Usage example:
  retask customer profile get

Output fields: customer_id, name, email, timezone, appearance_settings`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            c, err := svc.GetMyProfile(context.Background(), &commonv1.Empty{})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, c)
        },
    }
}

func newProfileSetCommand(gf *flags.Global) *cobra.Command {
    var name, email, timezone, theme string
    cmd := &cobra.Command{
        Use:   "profile set",
        Short: "Update your profile",
        Long: `Update the authenticated user's customer profile.

Usage example:
  retask customer profile set --name "Alice" --timezone "America/New_York" --theme THEME_PREFERENCE_DARK

Flags:
  --name string      Display name
  --email string     Email address
  --timezone string  IANA timezone (e.g. America/New_York)
  --theme string     Theme. Values: THEME_PREFERENCE_LIGHT, THEME_PREFERENCE_DARK, THEME_PREFERENCE_SYSTEM`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            existing, err := svc.GetMyProfile(context.Background(), &commonv1.Empty{})
            if err != nil {
                return err
            }
            if cmd.Flags().Changed("name") {
                existing.Name = name
            }
            if cmd.Flags().Changed("email") {
                existing.Email = email
            }
            if cmd.Flags().Changed("timezone") {
                existing.Timezone = timezone
            }
            if cmd.Flags().Changed("theme") {
                v, ok := customerv1.AppearanceSettings_ThemePreference_value[theme]
                if !ok {
                    return fmt.Errorf("invalid --theme %q. Values: THEME_PREFERENCE_LIGHT, THEME_PREFERENCE_DARK, THEME_PREFERENCE_SYSTEM", theme)
                }
                if existing.AppearanceSettings == nil {
                    existing.AppearanceSettings = &customerv1.AppearanceSettings{}
                }
                existing.AppearanceSettings.Theme = customerv1.AppearanceSettings_ThemePreference(v)
            }
            id, err := svc.SetMyProfile(context.Background(), existing)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"customer_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&name, "name", "", "Display name")
    cmd.Flags().StringVar(&email, "email", "", "Email address")
    cmd.Flags().StringVar(&timezone, "timezone", "", "IANA timezone")
    cmd.Flags().StringVar(&theme, "theme", "", "Theme: THEME_PREFERENCE_LIGHT, THEME_PREFERENCE_DARK, THEME_PREFERENCE_SYSTEM")
    return cmd
}

func newListCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "list",
        Short: "List customers (admin only)",
        Long: `List all customers. Requires admin access.

Usage example:
  retask customer list

Output fields: customer_id, name, email, created_at`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            resp, err := svc.GetCustomers(context.Background(), &customerv1.CustomersRequest{})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Customers)
        },
    }
}

func newGetCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "get <customer-id>",
        Short: "Get a customer by ID (admin only)",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            c, err := svc.GetCustomer(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, c)
        },
    }
}
```

- [ ] **Step 2: Register in main.go**

```go
import customercmd "nweb.xyz/retask-cli/internal/cmd/customer"
// root.AddCommand(customercmd.NewCommand(gf))
```

- [ ] **Step 3: Build and smoke test**

```bash
go build ./cmd/retask/ && ./retask customer --help
```

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/customer/ cmd/retask/main.go
git commit -m "feat: add customer commands"
```

---

## Task 12: project commands

**Files:**
- Create: `internal/cmd/project/command.go`
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Write project/command.go**

```go
// internal/cmd/project/command.go
package project

import (
    "context"
    "fmt"

    "github.com/spf13/cobra"
    "nweb.xyz/retask-cli/internal/auth"
    "nweb.xyz/retask-cli/internal/client"
    "nweb.xyz/retask-cli/internal/config"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/output"
    commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
    projectv1 "nweb.xyz/retask-cli/proto-gen/project/v1"
)

func NewCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "project",
        Short: "Manage projects and project members",
    }
    cmd.AddCommand(
        newListCommand(gf),
        newGetCommand(gf),
        newCreateCommand(gf),
        newUpdateCommand(gf),
        newArchiveCommand(gf),
        newUnarchiveCommand(gf),
        newDeleteCommand(gf),
        newMemberCommand(gf),
    )
    return cmd
}

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
    conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
    if err != nil {
        return nil, nil, err
    }
    return projectv1.NewProjectServiceClient(conn), func() { conn.Close() }, nil
}

func resolveWorkspaceID(gf *flags.Global) string {
    if gf.WorkspaceID != "" {
        return gf.WorkspaceID
    }
    path := gf.ConfigPath
    if path == "" {
        path = config.DefaultConfigPath()
    }
    cfg, _ := config.Load(path)
    if cfg != nil {
        return cfg.ActiveProfileData(gf.Profile).WorkspaceID
    }
    return ""
}

func newListCommand(gf *flags.Global) *cobra.Command {
    var archived bool
    cmd := &cobra.Command{
        Use:   "list",
        Short: "List projects in the active workspace",
        Long: `List projects in the workspace.

Usage example:
  retask project list
  retask project list --archived

Flags:
  --archived  Include only archived projects

Output fields: project_id, key, name, description, visibility, is_archived, created_at`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            req := &projectv1.ProjectsRequest{
                Filter: &projectv1.ProjectsRequest_Filter{
                    WorkspaceId: resolveWorkspaceID(gf),
                },
            }
            if cmd.Flags().Changed("archived") {
                v := commonv1.YesNo_YES
                if !archived {
                    v = commonv1.YesNo_NO
                }
                req.Filter.IsArchived = v
            }
            resp, err := svc.GetProjects(context.Background(), req)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Projects)
        },
    }
    cmd.Flags().BoolVar(&archived, "archived", false, "Show only archived projects")
    return cmd
}

func newGetCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "get <project-id>",
        Short: "Get a project by ID",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            p, err := svc.GetProject(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, p)
        },
    }
}

func newCreateCommand(gf *flags.Global) *cobra.Command {
    var name, description, visibility, color, icon string
    cmd := &cobra.Command{
        Use:   "create",
        Short: "Create a new project",
        Long: `Create a project in the active workspace.

Usage example:
  retask project create --name "Backend" --description "API services" --visibility VISIBILITY_WORKSPACE_EDIT

Flags:
  --name string         Required. Project name
  --description string  Project description
  --visibility string   Values: VISIBILITY_WORKSPACE_EDIT, VISIBILITY_WORKSPACE_VIEW, VISIBILITY_RESTRICTED
  --color string        Hex color (e.g. #3B82F6)
  --icon string         Icon identifier`,
        RunE: func(cmd *cobra.Command, args []string) error {
            if name == "" {
                return fmt.Errorf("--name is required")
            }
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            proj := &projectv1.Project{
                WorkspaceId: resolveWorkspaceID(gf),
                Name:        name,
                Description: description,
                Color:       color,
                Icon:        icon,
            }
            if visibility != "" {
                v, ok := projectv1.Visibility_value[visibility]
                if !ok {
                    return fmt.Errorf("invalid --visibility. Values: VISIBILITY_WORKSPACE_EDIT, VISIBILITY_WORKSPACE_VIEW, VISIBILITY_RESTRICTED")
                }
                proj.Visibility = projectv1.Visibility(v)
            }
            id, err := svc.SetProject(context.Background(), proj)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"project_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&name, "name", "", "Project name (required)")
    cmd.Flags().StringVar(&description, "description", "", "Description")
    cmd.Flags().StringVar(&visibility, "visibility", "", "Visibility: VISIBILITY_WORKSPACE_EDIT, VISIBILITY_WORKSPACE_VIEW, VISIBILITY_RESTRICTED")
    cmd.Flags().StringVar(&color, "color", "", "Hex color")
    cmd.Flags().StringVar(&icon, "icon", "", "Icon identifier")
    return cmd
}

func newUpdateCommand(gf *flags.Global) *cobra.Command {
    var name, description, visibility, color, icon string
    cmd := &cobra.Command{
        Use:   "update <project-id>",
        Short: "Update project fields",
        Long: `Update a project. Only provided flags are changed.

Usage example:
  retask project update proj_abc --name "New Name" --visibility VISIBILITY_RESTRICTED

Flags:
  --name string         New name
  --description string  New description
  --visibility string   Values: VISIBILITY_WORKSPACE_EDIT, VISIBILITY_WORKSPACE_VIEW, VISIBILITY_RESTRICTED
  --color string        New hex color
  --icon string         New icon`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            proj, err := svc.GetProject(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            if cmd.Flags().Changed("name") {
                proj.Name = name
            }
            if cmd.Flags().Changed("description") {
                proj.Description = description
            }
            if cmd.Flags().Changed("visibility") {
                v, ok := projectv1.Visibility_value[visibility]
                if !ok {
                    return fmt.Errorf("invalid --visibility. Values: VISIBILITY_WORKSPACE_EDIT, VISIBILITY_WORKSPACE_VIEW, VISIBILITY_RESTRICTED")
                }
                proj.Visibility = projectv1.Visibility(v)
            }
            if cmd.Flags().Changed("color") {
                proj.Color = color
            }
            if cmd.Flags().Changed("icon") {
                proj.Icon = icon
            }
            id, err := svc.SetProject(context.Background(), proj)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"project_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&name, "name", "", "New name")
    cmd.Flags().StringVar(&description, "description", "", "New description")
    cmd.Flags().StringVar(&visibility, "visibility", "", "Visibility")
    cmd.Flags().StringVar(&color, "color", "", "New hex color")
    cmd.Flags().StringVar(&icon, "icon", "", "New icon")
    return cmd
}

func newArchiveCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "archive <project-id>", Short: "Archive a project", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.ArchiveProject(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "archived", "project_id": args[0]})
        },
    }
}

func newUnarchiveCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "unarchive <project-id>", Short: "Unarchive a project", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.UnarchiveProject(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "unarchived", "project_id": args[0]})
        },
    }
}

func newDeleteCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "delete <project-id>", Short: "Delete a project", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.DeleteProject(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "deleted", "project_id": args[0]})
        },
    }
}

func newMemberCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{Use: "member", Short: "Manage project members"}
    cmd.AddCommand(newMemberListCmd(gf), newMemberAddCmd(gf), newMemberRemoveCmd(gf))
    return cmd
}

func newMemberListCmd(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "list <project-id>", Short: "List project members", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            resp, err := svc.GetProjectMembers(context.Background(), &projectv1.ProjectMembersRequest{ProjectId: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Members)
        },
    }
}

func newMemberAddCmd(gf *flags.Global) *cobra.Command {
    var memberID, role string
    cmd := &cobra.Command{
        Use:   "add <project-id>",
        Short: "Add a member to a project",
        Long: `Add a workspace member to a project.

Usage example:
  retask project member add proj_abc --member-id mem_xyz --role MEMBER_ROLE_EDITOR

Flags:
  --member-id string  Required. Workspace member ID
  --role string       Values: MEMBER_ROLE_VIEWER, MEMBER_ROLE_EDITOR, MEMBER_ROLE_ADMIN`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            if memberID == "" {
                return fmt.Errorf("--member-id is required")
            }
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            pm := &projectv1.ProjectMember{ProjectId: args[0]}
            if role != "" {
                v, ok := projectv1.MemberRole_value[role]
                if !ok {
                    return fmt.Errorf("invalid --role. Values: MEMBER_ROLE_VIEWER, MEMBER_ROLE_EDITOR, MEMBER_ROLE_ADMIN")
                }
                pm.Role = projectv1.MemberRole(v)
            }
            _, err = svc.SetProjectMember(context.Background(), pm)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "added", "project_id": args[0]})
        },
    }
    cmd.Flags().StringVar(&memberID, "member-id", "", "Workspace member ID (required)")
    cmd.Flags().StringVar(&role, "role", "", "Role: MEMBER_ROLE_VIEWER, MEMBER_ROLE_EDITOR, MEMBER_ROLE_ADMIN")
    return cmd
}

func newMemberRemoveCmd(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "remove <project-id> <member-id>", Short: "Remove a member from a project", Args: cobra.ExactArgs(2),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.RemoveProjectMember(context.Background(), &projectv1.RemoveProjectMemberRequest{
                ProjectId: args[0], ProjectMemberId: args[1],
            })
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "removed"})
        },
    }
}
```

- [ ] **Step 2: Register in main.go**

```go
import projectcmd "nweb.xyz/retask-cli/internal/cmd/project"
// root.AddCommand(projectcmd.NewCommand(gf))
```

- [ ] **Step 3: Build and smoke test**

```bash
go build ./cmd/retask/ && ./retask project --help
```

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/project/ cmd/retask/main.go
git commit -m "feat: add project commands"
```

---

## Task 13: file commands

**Files:**
- Create: `internal/cmd/file/command.go`
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Write file/command.go**

```go
// internal/cmd/file/command.go
package file

import (
    "context"
    "fmt"
    "time"

    "github.com/spf13/cobra"
    "google.golang.org/protobuf/types/known/durationpb"
    "nweb.xyz/retask-cli/internal/auth"
    "nweb.xyz/retask-cli/internal/client"
    "nweb.xyz/retask-cli/internal/config"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/output"
    commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
    filev1 "nweb.xyz/retask-cli/proto-gen/file/v1"
)

func NewCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "file",
        Short: "Manage files",
    }
    cmd.AddCommand(newListCommand(gf), newGetCommand(gf), newDeleteCommand(gf), newSignedURLCommand(gf))
    return cmd
}

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
    conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
    if err != nil {
        return nil, nil, err
    }
    return filev1.NewFileServiceClient(conn), func() { conn.Close() }, nil
}

func newListCommand(gf *flags.Global) *cobra.Command {
    var projectID string
    cmd := &cobra.Command{
        Use:   "list",
        Short: "List files",
        Long: `List files, optionally filtered by project.

Usage example:
  retask file list --project-id proj_abc

Flags:
  --project-id string  Filter by project

Output fields: file_id, name, size, content_type, created_at`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            req := &filev1.FilesRequest{Filter: &filev1.FilesRequest_Filter{}}
            if projectID != "" {
                req.Filter.ProjectId = projectID
            }
            resp, err := svc.GetFiles(context.Background(), req)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Files)
        },
    }
    cmd.Flags().StringVar(&projectID, "project-id", "", "Filter by project ID")
    return cmd
}

func newGetCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "get <file-id>", Short: "Get a file by ID", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            f, err := svc.GetFile(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, f)
        },
    }
}

func newDeleteCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "delete <file-id>", Short: "Delete a file", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.DeleteFile(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "deleted", "file_id": args[0]})
        },
    }
}

func newSignedURLCommand(gf *flags.Global) *cobra.Command {
    var expiresIn string
    cmd := &cobra.Command{
        Use:   "signed-url <file-id>",
        Short: "Get a signed download URL for a file",
        Long: `Generate a signed download URL for a file.

Usage example:
  retask file signed-url file_abc --expires-in 1h

Flags:
  --expires-in string  Expiry duration (e.g. 1h, 30m, 24h). Default: 1h

Output fields: file_id, signed_url, expires_at`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            d := time.Hour
            if expiresIn != "" {
                d, err = time.ParseDuration(expiresIn)
                if err != nil {
                    return fmt.Errorf("--expires-in must be a Go duration (e.g. 1h, 30m): %w", err)
                }
            }
            resp, err := svc.GetFileSignedUrl(context.Background(), &filev1.FileSignedUrlRequest{
                FileId:    args[0],
                ExpiresIn: durationpb.New(d),
            })
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp)
        },
    }
    cmd.Flags().StringVar(&expiresIn, "expires-in", "1h", "Expiry duration (e.g. 1h, 30m)")
    return cmd
}
```

- [ ] **Step 2: Register in main.go**

```go
import filecmd "nweb.xyz/retask-cli/internal/cmd/file"
// root.AddCommand(filecmd.NewCommand(gf))
```

- [ ] **Step 3: Build and smoke test**

```bash
go build ./cmd/retask/ && ./retask file --help
```

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/file/ cmd/retask/main.go
git commit -m "feat: add file commands"
```

---

## Task 14: integration commands

**Files:**
- Create: `internal/cmd/integration/command.go`
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Write integration/command.go**

```go
// internal/cmd/integration/command.go
package integration

import (
    "context"
    "fmt"

    "github.com/spf13/cobra"
    "nweb.xyz/retask-cli/internal/auth"
    "nweb.xyz/retask-cli/internal/client"
    "nweb.xyz/retask-cli/internal/config"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/output"
    commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
    integrationv1 "nweb.xyz/retask-cli/proto-gen/integration/v1"
)

func NewCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "integration",
        Short: "Manage integrations and providers",
    }
    cmd.AddCommand(
        newProviderCommand(gf),
        newListCommand(gf),
        newGetCommand(gf),
        newSetCommand(gf),
        newDeleteCommand(gf),
        newGithubCommand(gf),
    )
    return cmd
}

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
    conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
    if err != nil {
        return nil, nil, err
    }
    return integrationv1.NewIntegrationServiceClient(conn), func() { conn.Close() }, nil
}

func resolveWorkspaceID(gf *flags.Global) string {
    if gf.WorkspaceID != "" {
        return gf.WorkspaceID
    }
    path := gf.ConfigPath
    if path == "" {
        path = config.DefaultConfigPath()
    }
    cfg, _ := config.Load(path)
    if cfg != nil {
        return cfg.ActiveProfileData(gf.Profile).WorkspaceID
    }
    return ""
}

func newProviderCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{Use: "provider", Short: "List and get integration providers"}
    cmd.AddCommand(
        &cobra.Command{
            Use: "list", Short: "List available integration providers",
            Long: `List all integration providers (GitHub, Anthropic, OpenAI, etc.).

Usage example:
  retask integration provider list

Output fields: provider_id, name, disable_oauth_flow, disable_access_token`,
            RunE: func(cmd *cobra.Command, args []string) error {
                svc, close, err := connect(gf)
                if err != nil {
                    return err
                }
                defer close()
                resp, err := svc.GetProviders(context.Background(), &integrationv1.ProvidersRequest{})
                if err != nil {
                    return err
                }
                return output.Print(gf.Pretty, resp.Providers)
            },
        },
        &cobra.Command{
            Use: "get <provider-id>", Short: "Get an integration provider by ID", Args: cobra.ExactArgs(1),
            RunE: func(cmd *cobra.Command, args []string) error {
                svc, close, err := connect(gf)
                if err != nil {
                    return err
                }
                defer close()
                p, err := svc.GetProvider(context.Background(), &commonv1.Id{Id: args[0]})
                if err != nil {
                    return err
                }
                return output.Print(gf.Pretty, p)
            },
        },
    )
    return cmd
}

func newListCommand(gf *flags.Global) *cobra.Command {
    var providerID string
    cmd := &cobra.Command{
        Use:   "list",
        Short: "List integrations in the workspace",
        Long: `List integrations for the workspace.

Usage example:
  retask integration list
  retask integration list --provider-id github

Flags:
  --provider-id string  Filter by provider (e.g. github, anthropic)

Output fields: integration_id, provider_id, level, access_level, created_at`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            f := &integrationv1.IntegrationsRequest_Filter{
                WorkspaceId: resolveWorkspaceID(gf),
            }
            if providerID != "" {
                f.ProviderIds = []string{providerID}
            }
            resp, err := svc.GetIntegrations(context.Background(), &integrationv1.IntegrationsRequest{Filter: f})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Integrations)
        },
    }
    cmd.Flags().StringVar(&providerID, "provider-id", "", "Filter by provider ID")
    return cmd
}

func newGetCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "get <integration-id>", Short: "Get an integration by ID", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            i, err := svc.GetIntegration(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, i)
        },
    }
}

func newSetCommand(gf *flags.Global) *cobra.Command {
    var providerID, level, accessToken string
    cmd := &cobra.Command{
        Use:   "set",
        Short: "Create or update an integration",
        Long: `Create or update an integration for a provider. Uniqueness on (workspace, provider, level).

Usage example:
  retask integration set --provider-id github --level LEVEL_MEMBER --access-token ghp_xxx

Flags:
  --provider-id string   Required. Provider ID (e.g. github, anthropic)
  --level string         Values: LEVEL_WORKSPACE, LEVEL_MEMBER (default: LEVEL_MEMBER)
  --access-token string  Required. The provider access token (stored securely in secret manager)`,
        RunE: func(cmd *cobra.Command, args []string) error {
            if providerID == "" {
                return fmt.Errorf("--provider-id is required")
            }
            if accessToken == "" {
                return fmt.Errorf("--access-token is required")
            }
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            intg := &integrationv1.Integration{
                WorkspaceId: resolveWorkspaceID(gf),
                ProviderId:  providerID,
                Credentials: &integrationv1.Integration_Credentials{AccessToken: accessToken},
            }
            if level != "" {
                v, ok := integrationv1.Integration_Level_value[level]
                if !ok {
                    return fmt.Errorf("invalid --level. Values: LEVEL_WORKSPACE, LEVEL_MEMBER")
                }
                intg.Level = integrationv1.Integration_Level(v)
            }
            id, err := svc.SetIntegration(context.Background(), intg)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"integration_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&providerID, "provider-id", "", "Provider ID (required)")
    cmd.Flags().StringVar(&level, "level", "", "Level: LEVEL_WORKSPACE, LEVEL_MEMBER")
    cmd.Flags().StringVar(&accessToken, "access-token", "", "Provider access token (required)")
    return cmd
}

func newDeleteCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "delete <integration-id>", Short: "Delete an integration", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.DeleteIntegration(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "deleted", "integration_id": args[0]})
        },
    }
}

func newGithubCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{Use: "github", Short: "GitHub-specific integration helpers"}
    var level string
    reposCmd := &cobra.Command{
        Use:   "repos",
        Short: "List GitHub repos accessible via the integration",
        Long: `List GitHub repositories accessible via the connected GitHub integration.

Usage example:
  retask integration github repos
  retask integration github repos --level LEVEL_WORKSPACE

Flags:
  --level string  Integration level. Values: LEVEL_WORKSPACE, LEVEL_MEMBER

Output fields: name, clone_url, default_branch, private`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            req := &integrationv1.GithubReposRequest{WorkspaceId: resolveWorkspaceID(gf)}
            if level != "" {
                v, ok := integrationv1.Integration_Level_value[level]
                if !ok {
                    return fmt.Errorf("invalid --level. Values: LEVEL_WORKSPACE, LEVEL_MEMBER")
                }
                req.Level = integrationv1.Integration_Level(v)
            }
            resp, err := svc.GetGithubRepos(context.Background(), req)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Repos)
        },
    }
    reposCmd.Flags().StringVar(&level, "level", "", "Level: LEVEL_WORKSPACE, LEVEL_MEMBER")
    cmd.AddCommand(reposCmd)
    return cmd
}
```

- [ ] **Step 2: Register in main.go**

```go
import integrationcmd "nweb.xyz/retask-cli/internal/cmd/integration"
// root.AddCommand(integrationcmd.NewCommand(gf))
```

- [ ] **Step 3: Build and smoke test**

```bash
go build ./cmd/retask/ && ./retask integration --help
```

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/integration/ cmd/retask/main.go
git commit -m "feat: add integration commands"
```

---

## Task 15: task commands

**Files:**
- Create: `internal/cmd/task/command.go`
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Write task/command.go**

```go
// internal/cmd/task/command.go
package task

import (
    "context"
    "fmt"
    "time"

    "github.com/spf13/cobra"
    "google.golang.org/protobuf/types/known/timestamppb"
    "nweb.xyz/retask-cli/internal/auth"
    "nweb.xyz/retask-cli/internal/client"
    "nweb.xyz/retask-cli/internal/config"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/output"
    commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
    taskv1 "nweb.xyz/retask-cli/proto-gen/retask/task/v1"
)

func NewCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "task",
        Short: "Manage Retask tasks",
    }
    cmd.AddCommand(
        newListCommand(gf),
        newGetCommand(gf),
        newGetByKeyCommand(gf),
        newCreateCommand(gf),
        newUpdateCommand(gf),
        newDeleteCommand(gf),
        newAttachmentCommand(gf),
    )
    return cmd
}

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
    conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
    if err != nil {
        return nil, nil, err
    }
    return taskv1.NewTaskServiceClient(conn), func() { conn.Close() }, nil
}

func resolveWorkspaceID(gf *flags.Global) string {
    if gf.WorkspaceID != "" {
        return gf.WorkspaceID
    }
    path := gf.ConfigPath
    if path == "" {
        path = config.DefaultConfigPath()
    }
    cfg, _ := config.Load(path)
    if cfg != nil {
        return cfg.ActiveProfileData(gf.Profile).WorkspaceID
    }
    return ""
}

func newListCommand(gf *flags.Global) *cobra.Command {
    var projectID, status, assignee, priority string
    cmd := &cobra.Command{
        Use:   "list",
        Short: "List tasks",
        Long: `List tasks in a project or workspace.

Usage example:
  retask task list --project-id proj_abc
  retask task list --project-id proj_abc --status STATUS_OPEN --priority HIGH

Flags:
  --project-id string  Filter by project ID
  --status string      Filter by status ID (from project config)
  --assignee string    Filter by assignee NRN (nweb:workspace:member:<uuid>), or "" for unassigned
  --priority string    Values: LOW, MEDIUM, HIGH, URGENT

Output fields: task_id, key, title, status, priority, assignee_nrns, due_at, created_at`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            f := &taskv1.TasksRequest_Filter{WorkspaceId: resolveWorkspaceID(gf)}
            if projectID != "" {
                f.ProjectIds = []string{projectID}
            }
            if status != "" {
                f.StatusIds = []string{status}
            }
            if assignee != "" {
                f.AssigneeNrns = []string{assignee}
            }
            if priority != "" {
                v, ok := taskv1.Task_Priority_value[priority]
                if !ok {
                    return fmt.Errorf("invalid --priority. Values: LOW, MEDIUM, HIGH, URGENT")
                }
                f.Priority = taskv1.Task_Priority(v)
            }
            resp, err := svc.GetTasks(context.Background(), &taskv1.TasksRequest{Filter: f})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Tasks)
        },
    }
    cmd.Flags().StringVar(&projectID, "project-id", "", "Filter by project ID")
    cmd.Flags().StringVar(&status, "status", "", "Filter by status ID")
    cmd.Flags().StringVar(&assignee, "assignee", "", "Filter by assignee NRN")
    cmd.Flags().StringVar(&priority, "priority", "", "Filter by priority: LOW, MEDIUM, HIGH, URGENT")
    return cmd
}

func newGetCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "get <task-id>", Short: "Get a task by ID", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            t, err := svc.GetTask(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, t)
        },
    }
}

func newGetByKeyCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "get-by-key <key>",
        Short: "Get a task by its project key (e.g. ENG-42)",
        Long: `Get a task by its short key within the workspace.

Usage example:
  retask task get-by-key ENG-42`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            t, err := svc.GetTaskByKey(context.Background(), &taskv1.TaskByKeyRequest{
                WorkspaceId: resolveWorkspaceID(gf),
                Key:         args[0],
            })
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, t)
        },
    }
}

func newCreateCommand(gf *flags.Global) *cobra.Command {
    var projectID, title, description, status, priority, assignee, dueAt string
    cmd := &cobra.Command{
        Use:   "create",
        Short: "Create a task",
        Long: `Create a new task in a project.

Usage example:
  retask task create --project-id proj_abc --title "Fix login bug" --priority HIGH
  retask task create --project-id proj_abc --title "Deploy" --due-at 2026-06-15T09:00:00Z

Flags:
  --project-id string   Required. Project to create the task in
  --title string        Required. Task title
  --description string  Task description (markdown)
  --status string       Status ID from project config (default: first status)
  --priority string     Values: LOW, MEDIUM, HIGH, URGENT
  --assignee string     Assignee NRN (nweb:workspace:member:<uuid>)
  --due-at string       Due date in RFC3339 (e.g. 2026-06-15T09:00:00Z)`,
        RunE: func(cmd *cobra.Command, args []string) error {
            if projectID == "" {
                return fmt.Errorf("--project-id is required")
            }
            if title == "" {
                return fmt.Errorf("--title is required")
            }
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            t := &taskv1.Task{
                ProjectId:   projectID,
                WorkspaceId: resolveWorkspaceID(gf),
                Title:       title,
                Description: description,
            }
            if priority != "" {
                v, ok := taskv1.Task_Priority_value[priority]
                if !ok {
                    return fmt.Errorf("invalid --priority. Values: LOW, MEDIUM, HIGH, URGENT")
                }
                t.Priority = taskv1.Task_Priority(v)
            }
            if dueAt != "" {
                parsed, err := time.Parse(time.RFC3339, dueAt)
                if err != nil {
                    return fmt.Errorf("--due-at must be RFC3339 (e.g. 2026-06-15T09:00:00Z): %w", err)
                }
                t.DueAt = timestamppb.New(parsed)
            }
            id, err := svc.SetTask(context.Background(), t)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"task_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&projectID, "project-id", "", "Project ID (required)")
    cmd.Flags().StringVar(&title, "title", "", "Task title (required)")
    cmd.Flags().StringVar(&description, "description", "", "Task description")
    cmd.Flags().StringVar(&status, "status", "", "Status ID from project config")
    cmd.Flags().StringVar(&priority, "priority", "", "Priority: LOW, MEDIUM, HIGH, URGENT")
    cmd.Flags().StringVar(&assignee, "assignee", "", "Assignee NRN (nweb:workspace:member:<uuid>)")
    cmd.Flags().StringVar(&dueAt, "due-at", "", "Due date in RFC3339")
    return cmd
}

func newUpdateCommand(gf *flags.Global) *cobra.Command {
    var title, description, status, priority, assignee, dueAt string
    cmd := &cobra.Command{
        Use:   "update <task-id>",
        Short: "Update a task (partial update — only set flags are changed)",
        Long: `Update task fields. Only flags explicitly provided are changed.

Usage example:
  retask task update task_abc --status status_done_id --priority HIGH
  retask task update task_abc --assignee nweb:workspace:member:uuid --due-at 2026-06-15T09:00:00Z

Flags:
  --title string        New title
  --description string  New description
  --status string       Status ID from retask project config
  --priority string     Values: LOW, MEDIUM, HIGH, URGENT
  --assignee string     Assignee NRN (nweb:workspace:member:<uuid>)
  --due-at string       Due date in RFC3339 (e.g. 2026-06-15T09:00:00Z)`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            data := make(map[string]string)
            if cmd.Flags().Changed("title") {
                data["title"] = title
            }
            if cmd.Flags().Changed("description") {
                data["description"] = description
            }
            if cmd.Flags().Changed("status") {
                data["status"] = status
            }
            if cmd.Flags().Changed("priority") {
                if _, ok := taskv1.Task_Priority_value[priority]; !ok {
                    return fmt.Errorf("invalid --priority. Values: LOW, MEDIUM, HIGH, URGENT")
                }
                data["priority"] = priority
            }
            if cmd.Flags().Changed("assignee") {
                data["assignee_nrns"] = assignee
            }
            if cmd.Flags().Changed("due-at") {
                if _, err := time.Parse(time.RFC3339, dueAt); err != nil {
                    return fmt.Errorf("--due-at must be RFC3339: %w", err)
                }
                data["due_at"] = dueAt
            }
            if len(data) == 0 {
                return fmt.Errorf("no fields to update — provide at least one flag")
            }
            id, err := svc.SetPartialTask(context.Background(), &commonv1.PartialData{
                Id:   args[0],
                Data: data,
            })
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"task_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&title, "title", "", "New title")
    cmd.Flags().StringVar(&description, "description", "", "New description")
    cmd.Flags().StringVar(&status, "status", "", "Status ID (from retask project config)")
    cmd.Flags().StringVar(&priority, "priority", "", "Priority: LOW, MEDIUM, HIGH, URGENT")
    cmd.Flags().StringVar(&assignee, "assignee", "", "Assignee NRN (nweb:workspace:member:<uuid>)")
    cmd.Flags().StringVar(&dueAt, "due-at", "", "Due date in RFC3339")
    return cmd
}

func newDeleteCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "delete <task-id>", Short: "Delete a task", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.DeleteTask(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "deleted", "task_id": args[0]})
        },
    }
}

func newAttachmentCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{Use: "attachment", Short: "Manage task attachments"}
    cmd.AddCommand(
        &cobra.Command{
            Use: "add <task-id> <file-id>", Short: "Attach a file to a task", Args: cobra.ExactArgs(2),
            RunE: func(cmd *cobra.Command, args []string) error {
                svc, close, err := connect(gf)
                if err != nil {
                    return err
                }
                defer close()
                t, err := svc.AddTaskAttachment(context.Background(), &taskv1.AddTaskAttachmentRequest{
                    TaskId: args[0], FileId: args[1],
                })
                if err != nil {
                    return err
                }
                return output.Print(gf.Pretty, t)
            },
        },
        &cobra.Command{
            Use: "remove <task-id> <file-id>", Short: "Remove a file attachment from a task", Args: cobra.ExactArgs(2),
            RunE: func(cmd *cobra.Command, args []string) error {
                svc, close, err := connect(gf)
                if err != nil {
                    return err
                }
                defer close()
                t, err := svc.DeleteTaskAttachment(context.Background(), &taskv1.DeleteTaskAttachmentRequest{
                    TaskId: args[0], FileId: args[1],
                })
                if err != nil {
                    return err
                }
                return output.Print(gf.Pretty, t)
            },
        },
    )
    return cmd
}
```

- [ ] **Step 2: Register in main.go**

```go
import taskcmd "nweb.xyz/retask-cli/internal/cmd/task"
// root.AddCommand(taskcmd.NewCommand(gf))
```

- [ ] **Step 3: Build and smoke test**

```bash
go build ./cmd/retask/ && ./retask task --help && ./retask task update --help
```

Expected: `update` help shows all flags with enum values.

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/task/ cmd/retask/main.go
git commit -m "feat: add task commands (including SetPartialTask)"
```

---

## Task 16: project-config commands

**Files:**
- Create: `internal/cmd/projectconfig/command.go`
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Write projectconfig/command.go**

```go
// internal/cmd/projectconfig/command.go
package projectconfig

import (
    "context"

    "github.com/spf13/cobra"
    "nweb.xyz/retask-cli/internal/auth"
    "nweb.xyz/retask-cli/internal/client"
    "nweb.xyz/retask-cli/internal/config"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/output"
    commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
    retaskprojectv1 "nweb.xyz/retask-cli/proto-gen/retask/project/v1"
)

func NewCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "project-config",
        Short: "Manage Retask project configuration (task statuses, types, kanban)",
    }
    cmd.AddCommand(newGetCommand(gf), newExternalProjectCommand(gf))
    return cmd
}

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
    conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
    if err != nil {
        return nil, nil, err
    }
    return retaskprojectv1.NewRetaskProjectServiceClient(conn), func() { conn.Close() }, nil
}

func newGetCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "get <project-id>",
        Short: "Get Retask project configuration (statuses, types, kanban layout)",
        Long: `Get the Retask project configuration including task statuses, task types, and kanban settings.

Usage example:
  retask project-config get proj_abc

Output fields: project_id, task_statuses, task_types, kanban_config, default_task_view`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            cfg, err := svc.GetProjectConfig(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, cfg)
        },
    }
}

func newExternalProjectCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "external-project",
        Short: "Manage external project integrations (GitHub repos linked to projects)",
    }
    cmd.AddCommand(newExtListCommand(gf), newExtDeleteCommand(gf))
    return cmd
}

func newExtListCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "list <project-id>",
        Short: "List external projects linked to a Retask project",
        Long: `List external projects (e.g. GitHub repos) linked to a Retask project.

Usage example:
  retask project-config external-project list proj_abc

Output fields: external_project_id, source_type, source_id, name, last_sync_at`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            resp, err := svc.GetExternalProjects(context.Background(), &retaskprojectv1.ExternalProjectsRequest{
                ProjectId: args[0],
            })
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.ExternalProjects)
        },
    }
}

func newExtDeleteCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "delete <external-project-id>", Short: "Delete an external project link", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.DeleteExternalProject(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "deleted", "external_project_id": args[0]})
        },
    }
}
```

- [ ] **Step 2: Register in main.go**

```go
import projectconfigcmd "nweb.xyz/retask-cli/internal/cmd/projectconfig"
// root.AddCommand(projectconfigcmd.NewCommand(gf))
```

- [ ] **Step 3: Build and smoke test**

```bash
go build ./cmd/retask/ && ./retask project-config --help
```

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/projectconfig/ cmd/retask/main.go
git commit -m "feat: add project-config commands"
```

---

## Task 17: sandbox commands

**Files:**
- Create: `internal/cmd/sandbox/command.go`
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Write sandbox/command.go**

```go
// internal/cmd/sandbox/command.go
package sandbox

import (
    "context"
    "fmt"

    "github.com/spf13/cobra"
    "nweb.xyz/retask-cli/internal/auth"
    "nweb.xyz/retask-cli/internal/client"
    "nweb.xyz/retask-cli/internal/config"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/output"
    commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
    sandboxv1 "nweb.xyz/retask-cli/proto-gen/retask/sandbox/v1"
)

func NewCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "sandbox",
        Short: "Manage Retask sandboxes and sessions",
    }
    cmd.AddCommand(
        newListCommand(gf),
        newGetCommand(gf),
        newCreateCommand(gf),
        newUpdateCommand(gf),
        newStopCommand(gf),
        newDeleteCommand(gf),
        newSessionCommand(gf),
    )
    return cmd
}

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
    conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
    if err != nil {
        return nil, nil, err
    }
    return sandboxv1.NewSandboxServiceClient(conn), func() { conn.Close() }, nil
}

func resolveWorkspaceID(gf *flags.Global) string {
    if gf.WorkspaceID != "" {
        return gf.WorkspaceID
    }
    path := gf.ConfigPath
    if path == "" {
        path = config.DefaultConfigPath()
    }
    cfg, _ := config.Load(path)
    if cfg != nil {
        return cfg.ActiveProfileData(gf.Profile).WorkspaceID
    }
    return ""
}

func newListCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use:   "list",
        Short: "List sandboxes in the workspace",
        Long: `List sandboxes in the active workspace.

Usage example:
  retask sandbox list

Output fields: sandbox_id, name, status, type, created_at`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            resp, err := svc.GetSandboxes(context.Background(), &sandboxv1.SandboxesRequest{
                Filter: &sandboxv1.SandboxesRequest_Filter{WorkspaceId: resolveWorkspaceID(gf)},
            })
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Sandboxes)
        },
    }
}

func newGetCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "get <sandbox-id>", Short: "Get a sandbox by ID", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            s, err := svc.GetSandbox(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, s)
        },
    }
}

func newCreateCommand(gf *flags.Global) *cobra.Command {
    var name, templateID string
    cmd := &cobra.Command{
        Use:   "create",
        Short: "Create a sandbox",
        Long: `Create a new sandbox in the workspace.

Usage example:
  retask sandbox create --name "my-sandbox" --template-id tmpl_abc

Flags:
  --name string         Required. Sandbox display name
  --template-id string  Sandbox template ID to base configuration on`,
        RunE: func(cmd *cobra.Command, args []string) error {
            if name == "" {
                return fmt.Errorf("--name is required")
            }
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            sb := &sandboxv1.Sandbox{
                Name:        name,
                WorkspaceId: resolveWorkspaceID(gf),
            }
            if templateID != "" {
                sb.SandboxTemplateId = templateID
            }
            id, err := svc.SetSandbox(context.Background(), sb)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"sandbox_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&name, "name", "", "Sandbox name (required)")
    cmd.Flags().StringVar(&templateID, "template-id", "", "Template ID")
    return cmd
}

func newUpdateCommand(gf *flags.Global) *cobra.Command {
    var name string
    cmd := &cobra.Command{
        Use:   "update <sandbox-id>",
        Short: "Update a sandbox",
        Long: `Update sandbox fields. Only provided flags are changed.

Usage example:
  retask sandbox update sb_abc --name "new-name"

Flags:
  --name string  New sandbox name`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            sb, err := svc.GetSandbox(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            if cmd.Flags().Changed("name") {
                sb.Name = name
            }
            id, err := svc.SetSandbox(context.Background(), sb)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"sandbox_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&name, "name", "", "New sandbox name")
    return cmd
}

func newStopCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "stop <sandbox-id>", Short: "Stop a running sandbox", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.StopSandbox(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "stopped", "sandbox_id": args[0]})
        },
    }
}

func newDeleteCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "delete <sandbox-id>", Short: "Delete a sandbox", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.DeleteSandbox(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "deleted", "sandbox_id": args[0]})
        },
    }
}

func newSessionCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{Use: "session", Short: "Manage sandbox sessions"}
    cmd.AddCommand(
        newSessionListCommand(gf),
        newSessionGetCommand(gf),
        newSessionCreateCommand(gf),
        newSessionUpdateCommand(gf),
        newSessionStopCommand(gf),
        newSessionDeleteCommand(gf),
    )
    return cmd
}

func newSessionListCommand(gf *flags.Global) *cobra.Command {
    var sandboxID string
    cmd := &cobra.Command{
        Use:   "list",
        Short: "List sessions",
        Long: `List sessions in the workspace, optionally filtered by sandbox.

Usage example:
  retask sandbox session list
  retask sandbox session list --sandbox-id sb_abc

Output fields: session_id, sandbox_id, status, task_id, created_at`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            f := &sandboxv1.SessionsRequest_Filter{WorkspaceId: resolveWorkspaceID(gf)}
            if sandboxID != "" {
                f.SandboxIds = []string{sandboxID}
            }
            resp, err := svc.GetSessions(context.Background(), &sandboxv1.SessionsRequest{Filter: f})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Sessions)
        },
    }
    cmd.Flags().StringVar(&sandboxID, "sandbox-id", "", "Filter by sandbox ID")
    return cmd
}

func newSessionGetCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "get <session-id>", Short: "Get a session by ID", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            s, err := svc.GetSession(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, s)
        },
    }
}

func newSessionCreateCommand(gf *flags.Global) *cobra.Command {
    var sandboxID, taskID string
    cmd := &cobra.Command{
        Use:   "create",
        Short: "Create a new sandbox session",
        Long: `Start a new session on a sandbox.

Usage example:
  retask sandbox session create --sandbox-id sb_abc
  retask sandbox session create --sandbox-id sb_abc --task-id task_xyz

Flags:
  --sandbox-id string  Required. Sandbox to start the session on
  --task-id string     Optional. Associate the session with a task`,
        RunE: func(cmd *cobra.Command, args []string) error {
            if sandboxID == "" {
                return fmt.Errorf("--sandbox-id is required")
            }
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            req := &sandboxv1.NewSessionRequest{
                SandboxId:   sandboxID,
                WorkspaceId: resolveWorkspaceID(gf),
            }
            if taskID != "" {
                req.TaskId = taskID
            }
            id, err := svc.NewSession(context.Background(), req)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"session_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&sandboxID, "sandbox-id", "", "Sandbox ID (required)")
    cmd.Flags().StringVar(&taskID, "task-id", "", "Associate with a task")
    return cmd
}

func newSessionUpdateCommand(gf *flags.Global) *cobra.Command {
    var status string
    cmd := &cobra.Command{
        Use:   "update <session-id>",
        Short: "Update a session (partial update — only set flags are changed)",
        Long: `Update session fields via partial update.

Usage example:
  retask sandbox session update sess_abc --status SESSION_STATUS_IDLE

Flags:
  --status string  Session status. Values: SESSION_STATUS_STARTING, SESSION_STATUS_ACTIVE,
                   SESSION_STATUS_IDLE, SESSION_STATUS_STOPPING, SESSION_STATUS_STOPPED`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            data := make(map[string]string)
            if cmd.Flags().Changed("status") {
                data["status"] = status
            }
            if len(data) == 0 {
                return fmt.Errorf("no fields to update — provide at least one flag")
            }
            id, err := svc.SetPartialSession(context.Background(), &commonv1.PartialData{
                Id: args[0], Data: data,
            })
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"session_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&status, "status", "", "Session status: SESSION_STATUS_STARTING, SESSION_STATUS_ACTIVE, SESSION_STATUS_IDLE, SESSION_STATUS_STOPPING, SESSION_STATUS_STOPPED")
    return cmd
}

func newSessionStopCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "stop <session-id>", Short: "Stop a session", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.StopSession(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "stopped", "session_id": args[0]})
        },
    }
}

func newSessionDeleteCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "delete <session-id>", Short: "Delete a session", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.DeleteSession(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "deleted", "session_id": args[0]})
        },
    }
}
```

- [ ] **Step 2: Register in main.go**

```go
import sandboxcmd "nweb.xyz/retask-cli/internal/cmd/sandbox"
// root.AddCommand(sandboxcmd.NewCommand(gf))
```

- [ ] **Step 3: Build and smoke test**

```bash
go build ./cmd/retask/ && ./retask sandbox --help && ./retask sandbox session --help
```

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/sandbox/ cmd/retask/main.go
git commit -m "feat: add sandbox and session commands"
```

---

## Task 18: agent commands

**Files:**
- Create: `internal/cmd/agent/command.go`
- Modify: `cmd/retask/main.go`

- [ ] **Step 1: Write agent/command.go**

```go
// internal/cmd/agent/command.go
package agent

import (
    "context"
    "fmt"

    "github.com/spf13/cobra"
    "nweb.xyz/retask-cli/internal/auth"
    "nweb.xyz/retask-cli/internal/client"
    "nweb.xyz/retask-cli/internal/config"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/output"
    commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
    agentv1 "nweb.xyz/retask-cli/proto-gen/retask/agent/v1"
)

func NewCommand(gf *flags.Global) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "agent",
        Short: "Manage Retask agents",
    }
    cmd.AddCommand(
        newListCommand(gf),
        newGetCommand(gf),
        newCreateCommand(gf),
        newUpdateCommand(gf),
        newDeleteCommand(gf),
    )
    return cmd
}

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
    conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
    if err != nil {
        return nil, nil, err
    }
    return agentv1.NewAgentServiceClient(conn), func() { conn.Close() }, nil
}

func resolveWorkspaceID(gf *flags.Global) string {
    if gf.WorkspaceID != "" {
        return gf.WorkspaceID
    }
    path := gf.ConfigPath
    if path == "" {
        path = config.DefaultConfigPath()
    }
    cfg, _ := config.Load(path)
    if cfg != nil {
        return cfg.ActiveProfileData(gf.Profile).WorkspaceID
    }
    return ""
}

func newListCommand(gf *flags.Global) *cobra.Command {
    var role string
    cmd := &cobra.Command{
        Use:   "list",
        Short: "List agents in the workspace",
        Long: `List Retask agents in the active workspace.

Usage example:
  retask agent list
  retask agent list --role ROLE_TASK_PROCESSOR

Flags:
  --role string  Filter by role. Values: ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR

Output fields: agent_id, name, role, description, created_at`,
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            f := &agentv1.AgentsRequest_Filter{WorkspaceId: resolveWorkspaceID(gf)}
            if role != "" {
                v, ok := agentv1.Agent_Role_value[role]
                if !ok {
                    return fmt.Errorf("invalid --role. Values: ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR")
                }
                f.Roles = []agentv1.Agent_Role{agentv1.Agent_Role(v)}
            }
            resp, err := svc.GetAgents(context.Background(), &agentv1.AgentsRequest{Filter: f})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, resp.Agents)
        },
    }
    cmd.Flags().StringVar(&role, "role", "", "Filter by role: ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR")
    return cmd
}

func newGetCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "get <agent-id>", Short: "Get an agent by ID", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            a, err := svc.GetAgent(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, a)
        },
    }
}

func newCreateCommand(gf *flags.Global) *cobra.Command {
    var name, role, description, sandboxTemplateID string
    cmd := &cobra.Command{
        Use:   "create",
        Short: "Create an agent",
        Long: `Create a new Retask agent in the workspace.

Usage example:
  retask agent create --name "Task Bot" --role ROLE_TASK_PROCESSOR --description "Processes tasks automatically"

Flags:
  --name string                Required. Agent display name
  --role string                Agent role. Values: ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR
  --description string         Agent description
  --sandbox-template-id string Sandbox template ID for agent execution environment`,
        RunE: func(cmd *cobra.Command, args []string) error {
            if name == "" {
                return fmt.Errorf("--name is required")
            }
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            a := &agentv1.Agent{
                WorkspaceId:       resolveWorkspaceID(gf),
                Name:              name,
                Description:       description,
                SandboxTemplateId: sandboxTemplateID,
            }
            if role != "" {
                v, ok := agentv1.Agent_Role_value[role]
                if !ok {
                    return fmt.Errorf("invalid --role. Values: ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR")
                }
                a.Role = agentv1.Agent_Role(v)
            }
            id, err := svc.SetAgent(context.Background(), a)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"agent_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&name, "name", "", "Agent name (required)")
    cmd.Flags().StringVar(&role, "role", "", "Role: ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR")
    cmd.Flags().StringVar(&description, "description", "", "Description")
    cmd.Flags().StringVar(&sandboxTemplateID, "sandbox-template-id", "", "Sandbox template ID")
    return cmd
}

func newUpdateCommand(gf *flags.Global) *cobra.Command {
    var name, role, description, sandboxTemplateID string
    cmd := &cobra.Command{
        Use:   "update <agent-id>",
        Short: "Update an agent",
        Long: `Update agent fields. Only provided flags are changed.

Usage example:
  retask agent update agent_abc --name "New Name" --role ROLE_TASK_PLANNER

Flags:
  --name string                New name
  --role string                New role. Values: ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR
  --description string         New description
  --sandbox-template-id string New sandbox template ID`,
        Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            a, err := svc.GetAgent(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            if cmd.Flags().Changed("name") {
                a.Name = name
            }
            if cmd.Flags().Changed("description") {
                a.Description = description
            }
            if cmd.Flags().Changed("sandbox-template-id") {
                a.SandboxTemplateId = sandboxTemplateID
            }
            if cmd.Flags().Changed("role") {
                v, ok := agentv1.Agent_Role_value[role]
                if !ok {
                    return fmt.Errorf("invalid --role. Values: ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR")
                }
                a.Role = agentv1.Agent_Role(v)
            }
            id, err := svc.SetAgent(context.Background(), a)
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"agent_id": id.Id})
        },
    }
    cmd.Flags().StringVar(&name, "name", "", "New name")
    cmd.Flags().StringVar(&role, "role", "", "Role: ROLE_TASK_PLANNER, ROLE_TASK_PROCESSOR")
    cmd.Flags().StringVar(&description, "description", "", "New description")
    cmd.Flags().StringVar(&sandboxTemplateID, "sandbox-template-id", "", "New sandbox template ID")
    return cmd
}

func newDeleteCommand(gf *flags.Global) *cobra.Command {
    return &cobra.Command{
        Use: "delete <agent-id>", Short: "Delete an agent", Args: cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            svc, close, err := connect(gf)
            if err != nil {
                return err
            }
            defer close()
            _, err = svc.DeleteAgent(context.Background(), &commonv1.Id{Id: args[0]})
            if err != nil {
                return err
            }
            return output.Print(gf.Pretty, map[string]string{"status": "deleted", "agent_id": args[0]})
        },
    }
}
```

- [ ] **Step 2: Register in main.go**

```go
import agentcmd "nweb.xyz/retask-cli/internal/cmd/agent"
// root.AddCommand(agentcmd.NewCommand(gf))
```

- [ ] **Step 3: Build and smoke test**

```bash
go build ./cmd/retask/ && ./retask agent --help
```

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/agent/ cmd/retask/main.go
git commit -m "feat: add agent commands"
```

---

## Task 19: Wire all commands and final main.go

**Files:**
- Modify: `cmd/retask/main.go` (finalize all AddCommand calls)

- [ ] **Step 1: Write final main.go with all services wired**

```go
// cmd/retask/main.go
package main

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
    agentcmd        "nweb.xyz/retask-cli/internal/cmd/agent"
    authcmd         "nweb.xyz/retask-cli/internal/cmd/auth"
    customercmd     "nweb.xyz/retask-cli/internal/cmd/customer"
    filecmd         "nweb.xyz/retask-cli/internal/cmd/file"
    integrationcmd  "nweb.xyz/retask-cli/internal/cmd/integration"
    projectcmd      "nweb.xyz/retask-cli/internal/cmd/project"
    projectconfigcmd "nweb.xyz/retask-cli/internal/cmd/projectconfig"
    sandboxcmd      "nweb.xyz/retask-cli/internal/cmd/sandbox"
    taskcmd         "nweb.xyz/retask-cli/internal/cmd/task"
    workspacecmd    "nweb.xyz/retask-cli/internal/cmd/workspace"
    helpcmd         "nweb.xyz/retask-cli/internal/cmd/helpcmd"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/version"
)

func main() {
    if err := newRootCommand().Execute(); err != nil {
        os.Exit(1)
    }
}

func newRootCommand() *cobra.Command {
    gf := &flags.Global{}

    root := &cobra.Command{
        Use:          "retask",
        Short:        "Retask CLI — interact with NWEB Retask APIs",
        SilenceUsage: true,
        Version:      version.Version,
    }

    root.PersistentFlags().StringVar(&gf.Profile, "profile", "", "Config profile name (env: RETASK_PROFILE)")
    root.PersistentFlags().StringVar(&gf.WorkspaceID, "workspace-id", "", "Workspace ID (env: NWEB_WORKSPACE_ID)")
    root.PersistentFlags().BoolVar(&gf.Pretty, "pretty", false, "Human-readable table output (default: JSON)")
    root.PersistentFlags().BoolVar(&gf.Insecure, "insecure", false, "Skip TLS verification (local dev only)")
    root.PersistentFlags().BoolVar(&gf.NoSave, "no-save", false, "Don't write credentials to config file")
    root.PersistentFlags().StringVar(&gf.ConfigPath, "config", "", "Config file path (default: ~/.config/retask/config.yaml)")

    root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
        if gf.Profile == "" {
            gf.Profile = os.Getenv("RETASK_PROFILE")
        }
        if gf.WorkspaceID == "" {
            gf.WorkspaceID = os.Getenv("NWEB_WORKSPACE_ID")
        }
        if os.Getenv("RETASK_NO_PERSIST") != "" {
            gf.NoSave = true
        }
        return nil
    }

    root.SetVersionTemplate(fmt.Sprintf("retask version %s\n", version.Version))

    // Service commands — add one line here for each new service
    root.AddCommand(
        authcmd.NewCommand(gf),
        workspacecmd.NewCommand(gf),
        customercmd.NewCommand(gf),
        projectcmd.NewCommand(gf),
        filecmd.NewCommand(gf),
        integrationcmd.NewCommand(gf),
        taskcmd.NewCommand(gf),
        projectconfigcmd.NewCommand(gf),
        sandboxcmd.NewCommand(gf),
        agentcmd.NewCommand(gf),
        helpcmd.NewCommand(gf),
    )

    return root
}
```

- [ ] **Step 2: Build**

```bash
go build -ldflags "-X nweb.xyz/retask-cli/internal/version.Version=0.1.0" -o retask ./cmd/retask/
```

Expected: binary `retask` produced, no errors.

- [ ] **Step 3: Full smoke test**

```bash
./retask --version
./retask --help
./retask auth --help
./retask task update --help
./retask sandbox session --help
```

- [ ] **Step 4: Run all tests**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/retask/main.go
git commit -m "feat: wire all service commands into root"
```

---

## Task 20: AI support — help --llm and skill file

**Files:**
- Create: `internal/cmd/helpcmd/command.go`
- Create: `skills/retask-cli.md`

- [ ] **Step 1: Write helpcmd/command.go**

```go
// internal/cmd/helpcmd/command.go
package helpcmd

import (
    "context"
    "encoding/json"
    "fmt"
    "os"

    "github.com/spf13/cobra"
    "nweb.xyz/retask-cli/internal/flags"
    "nweb.xyz/retask-cli/internal/version"
)

func NewCommand(gf *flags.Global) *cobra.Command {
    var llm bool
    cmd := &cobra.Command{
        Use:   "help-llm",
        Short: "Print machine-readable command manifest for LLM injection",
        Long: `Print a JSON manifest of all retask CLI commands, flags, and examples.
Designed to be injected into an LLM system prompt or tool definition.

Usage example:
  retask help-llm
  retask help-llm | jq '.commands[] | select(.command | contains("task"))'`,
        RunE: func(cmd *cobra.Command, args []string) error {
            _ = llm
            manifest := buildManifest()
            enc := json.NewEncoder(os.Stdout)
            enc.SetIndent("", "  ")
            return enc.Encode(manifest)
        },
    }
    cmd.Flags().BoolVar(&llm, "llm", false, "alias for this command (deprecated)")
    return cmd
}

type commandEntry struct {
    Command     string   `json:"command"`
    Description string   `json:"description"`
    Flags       []string `json:"flags,omitempty"`
    Example     string   `json:"example"`
}

type manifest struct {
    CLI      string         `json:"cli"`
    Version  string         `json:"version"`
    Auth     authInfo       `json:"auth"`
    Commands []commandEntry `json:"commands"`
}

type authInfo struct {
    RequiredEnv []string `json:"required_env"`
    OptionalEnv []string `json:"optional_env"`
}

func buildManifest() manifest {
    _ = fmt.Sprintf // silence import
    _ = context.Background
    return manifest{
        CLI:     "retask",
        Version: version.Version,
        Auth: authInfo{
            RequiredEnv: []string{"NWEB_API_KEY", "NWEB_WORKSPACE_ID"},
            OptionalEnv: []string{"NWEB_API_TOKEN", "NWEB_API_ENDPOINT", "RETASK_PROFILE", "RETASK_NO_PERSIST"},
        },
        Commands: []commandEntry{
            {Command: "retask auth login", Description: "Exchange PAT for JWT, save to profile", Example: "retask auth login"},
            {Command: "retask auth logout", Description: "Clear cached JWT from active profile", Example: "retask auth logout"},
            {Command: "retask auth whoami", Description: "Print current token claims (workspace, expiry)", Example: "retask auth whoami"},
            {Command: "retask auth pat list", Description: "List PATs for current user", Example: "retask auth pat list"},
            {Command: "retask auth pat create", Description: "Create a new PAT", Flags: []string{"--name", "--description", "--expires-at"}, Example: "retask auth pat create --name ci-bot"},
            {Command: "retask auth pat revoke", Description: "Revoke a PAT by ID", Example: "retask auth pat revoke <pat-id>"},
            {Command: "retask workspace list", Description: "List workspaces accessible to the user", Example: "retask workspace list"},
            {Command: "retask workspace get", Description: "Get a workspace by ID", Example: "retask workspace get <workspace-id>"},
            {Command: "retask workspace create", Description: "Create a workspace", Flags: []string{"--name", "--description", "--color"}, Example: "retask workspace create --name 'My Team'"},
            {Command: "retask workspace update", Description: "Update workspace fields", Flags: []string{"--name", "--description", "--color"}, Example: "retask workspace update <id> --name 'New Name'"},
            {Command: "retask workspace delete", Description: "Delete a workspace", Example: "retask workspace delete <workspace-id>"},
            {Command: "retask workspace member list", Description: "List workspace members", Example: "retask workspace member list <workspace-id>"},
            {Command: "retask workspace member invite", Description: "Invite a member by email", Flags: []string{"--email", "--role", "--display-name"}, Example: "retask workspace member invite <ws-id> --email user@example.com --role EDITOR"},
            {Command: "retask workspace member update", Description: "Update member role or display name", Flags: []string{"--role", "--display-name"}, Example: "retask workspace member update <ws-id> <member-id> --role ADMIN"},
            {Command: "retask workspace member remove", Description: "Remove a member from workspace", Example: "retask workspace member remove <ws-id> <member-id>"},
            {Command: "retask customer profile get", Description: "Get your customer profile", Example: "retask customer profile get"},
            {Command: "retask customer profile set", Description: "Update your customer profile", Flags: []string{"--name", "--email", "--timezone", "--theme"}, Example: "retask customer profile set --name Alice --timezone America/New_York"},
            {Command: "retask project list", Description: "List projects in workspace", Flags: []string{"--archived"}, Example: "retask project list"},
            {Command: "retask project get", Description: "Get a project by ID", Example: "retask project get <project-id>"},
            {Command: "retask project create", Description: "Create a project", Flags: []string{"--name", "--description", "--visibility", "--color", "--icon"}, Example: "retask project create --name Backend --visibility VISIBILITY_WORKSPACE_EDIT"},
            {Command: "retask project update", Description: "Update project fields", Flags: []string{"--name", "--description", "--visibility", "--color", "--icon"}, Example: "retask project update <id> --name 'New Name'"},
            {Command: "retask project archive", Description: "Archive a project", Example: "retask project archive <project-id>"},
            {Command: "retask project unarchive", Description: "Unarchive a project", Example: "retask project unarchive <project-id>"},
            {Command: "retask project delete", Description: "Delete a project", Example: "retask project delete <project-id>"},
            {Command: "retask project member list", Description: "List project members", Example: "retask project member list <project-id>"},
            {Command: "retask project member add", Description: "Add a workspace member to a project", Flags: []string{"--member-id", "--role"}, Example: "retask project member add <proj-id> --member-id <mem-id> --role MEMBER_ROLE_EDITOR"},
            {Command: "retask project member remove", Description: "Remove a member from a project", Example: "retask project member remove <proj-id> <member-id>"},
            {Command: "retask file list", Description: "List files", Flags: []string{"--project-id"}, Example: "retask file list --project-id <proj-id>"},
            {Command: "retask file get", Description: "Get a file by ID", Example: "retask file get <file-id>"},
            {Command: "retask file delete", Description: "Delete a file", Example: "retask file delete <file-id>"},
            {Command: "retask file signed-url", Description: "Get a signed download URL", Flags: []string{"--expires-in"}, Example: "retask file signed-url <file-id> --expires-in 1h"},
            {Command: "retask integration provider list", Description: "List integration providers", Example: "retask integration provider list"},
            {Command: "retask integration list", Description: "List integrations", Flags: []string{"--provider-id"}, Example: "retask integration list --provider-id github"},
            {Command: "retask integration set", Description: "Create or update an integration", Flags: []string{"--provider-id", "--level", "--access-token"}, Example: "retask integration set --provider-id github --access-token ghp_xxx"},
            {Command: "retask integration delete", Description: "Delete an integration", Example: "retask integration delete <integration-id>"},
            {Command: "retask integration github repos", Description: "List GitHub repos accessible via integration", Flags: []string{"--level"}, Example: "retask integration github repos"},
            {Command: "retask task list", Description: "List tasks", Flags: []string{"--project-id", "--status", "--assignee", "--priority"}, Example: "retask task list --project-id <id> --priority HIGH"},
            {Command: "retask task get", Description: "Get a task by ID", Example: "retask task get <task-id>"},
            {Command: "retask task get-by-key", Description: "Get a task by key (e.g. ENG-42)", Example: "retask task get-by-key ENG-42"},
            {Command: "retask task create", Description: "Create a task", Flags: []string{"--project-id", "--title", "--description", "--status", "--priority", "--assignee", "--due-at"}, Example: "retask task create --project-id <id> --title 'Fix bug' --priority HIGH"},
            {Command: "retask task update", Description: "Partial update a task (only set flags change)", Flags: []string{"--title", "--description", "--status", "--priority", "--assignee", "--due-at"}, Example: "retask task update <id> --status <status-id> --priority HIGH"},
            {Command: "retask task delete", Description: "Delete a task", Example: "retask task delete <task-id>"},
            {Command: "retask task attachment add", Description: "Attach a file to a task", Example: "retask task attachment add <task-id> <file-id>"},
            {Command: "retask task attachment remove", Description: "Remove a file attachment from a task", Example: "retask task attachment remove <task-id> <file-id>"},
            {Command: "retask project-config get", Description: "Get Retask project config (statuses, types, kanban)", Example: "retask project-config get <project-id>"},
            {Command: "retask project-config external-project list", Description: "List external projects linked to a project", Example: "retask project-config external-project list <project-id>"},
            {Command: "retask project-config external-project delete", Description: "Delete an external project link", Example: "retask project-config external-project delete <external-project-id>"},
            {Command: "retask sandbox list", Description: "List sandboxes", Example: "retask sandbox list"},
            {Command: "retask sandbox get", Description: "Get a sandbox by ID", Example: "retask sandbox get <sandbox-id>"},
            {Command: "retask sandbox create", Description: "Create a sandbox", Flags: []string{"--name", "--template-id"}, Example: "retask sandbox create --name my-sandbox --template-id <tmpl-id>"},
            {Command: "retask sandbox stop", Description: "Stop a running sandbox", Example: "retask sandbox stop <sandbox-id>"},
            {Command: "retask sandbox delete", Description: "Delete a sandbox", Example: "retask sandbox delete <sandbox-id>"},
            {Command: "retask sandbox session list", Description: "List sessions", Flags: []string{"--sandbox-id"}, Example: "retask sandbox session list --sandbox-id <sb-id>"},
            {Command: "retask sandbox session get", Description: "Get a session by ID", Example: "retask sandbox session get <session-id>"},
            {Command: "retask sandbox session create", Description: "Create a sandbox session", Flags: []string{"--sandbox-id", "--task-id"}, Example: "retask sandbox session create --sandbox-id <sb-id>"},
            {Command: "retask sandbox session update", Description: "Partial update a session", Flags: []string{"--status"}, Example: "retask sandbox session update <id> --status SESSION_STATUS_IDLE"},
            {Command: "retask sandbox session stop", Description: "Stop a session", Example: "retask sandbox session stop <session-id>"},
            {Command: "retask sandbox session delete", Description: "Delete a session", Example: "retask sandbox session delete <session-id>"},
            {Command: "retask agent list", Description: "List agents", Flags: []string{"--role"}, Example: "retask agent list --role ROLE_TASK_PROCESSOR"},
            {Command: "retask agent get", Description: "Get an agent by ID", Example: "retask agent get <agent-id>"},
            {Command: "retask agent create", Description: "Create an agent", Flags: []string{"--name", "--role", "--description", "--sandbox-template-id"}, Example: "retask agent create --name 'Task Bot' --role ROLE_TASK_PROCESSOR"},
            {Command: "retask agent update", Description: "Update an agent", Flags: []string{"--name", "--role", "--description", "--sandbox-template-id"}, Example: "retask agent update <id> --name 'New Name'"},
            {Command: "retask agent delete", Description: "Delete an agent", Example: "retask agent delete <agent-id>"},
        },
    }
}
```

- [ ] **Step 2: Create skills/retask-cli.md**

```markdown
# retask CLI

Use `retask help-llm` to get a full JSON manifest of all commands, flags, and examples.

## Quick start

```bash
export NWEB_API_KEY="nweb_pat_..."
export NWEB_WORKSPACE_ID="ws_..."
retask auth login

# Or for session-isolated credentials (shared sandboxes):
eval $(retask auth login --no-save)
```

## Required env
- `NWEB_API_KEY` — PAT (Personal Access Token, starts with `nweb_pat_`)
- `NWEB_WORKSPACE_ID` — Workspace ID

## Optional env
- `NWEB_API_TOKEN` — Ready-to-use JWT (skips PAT exchange)
- `NWEB_API_ENDPOINT` — API endpoint (default: `api.dev.nweb.app:443`)
- `RETASK_PROFILE` — Config profile name (default: `default`)
- `RETASK_NO_PERSIST` — Don't write credentials to disk

## Output
All output is JSON by default. Add `--pretty` for human-readable tables.

## Discovery
```bash
retask help-llm           # full command manifest
retask <command> --help   # flags and examples for a specific command
```
```

- [ ] **Step 3: Build and verify help-llm output**

```bash
go build -ldflags "-X nweb.xyz/retask-cli/internal/version.Version=0.1.0" -o retask ./cmd/retask/
./retask help-llm | jq '.commands | length'
```

Expected: number equals total commands (should be 50+).

- [ ] **Step 4: Run all tests**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/helpcmd/ skills/retask-cli.md
git commit -m "feat: add help-llm command and skills/retask-cli.md"
```

---

## Task 21: Final build verification

- [ ] **Step 1: Build release binary**

```bash
go build \
  -ldflags "-X nweb.xyz/retask-cli/internal/version.Version=0.1.0" \
  -o retask \
  ./cmd/retask/
```

- [ ] **Step 2: Verify version**

```bash
./retask --version
```

Expected: `retask version 0.1.0`

- [ ] **Step 3: Verify all top-level commands present**

```bash
./retask --help
```

Expected: auth, workspace, customer, project, file, integration, task, project-config, sandbox, agent, help-llm all listed.

- [ ] **Step 4: Run full test suite**

```bash
go test ./... -v 2>&1 | tail -20
```

Expected: all PASS, no failures.

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "feat: retask CLI v0.1.0 complete"
```
