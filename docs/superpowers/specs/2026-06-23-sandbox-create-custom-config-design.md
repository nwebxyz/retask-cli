# Design: custom config on `retask sandbox create`

Date: 2026-06-23
Status: Approved

## Problem

`retask sandbox create` currently accepts only `--name`, `--type`, and
`--template-id`. To create a sandbox with a non-default `Sandbox.Config`, a
human must first author a `SandboxTemplate` and fork it with `--template-id`.
There is no way to set config inline for a one-off sandbox.

## Goal

Let a human operator configure a one-off sandbox directly on `create` via
ergonomic per-field flags, without first creating a template. Primary consumer
is a human at a terminal, so flags are favored over a JSON blob.

## Non-goals (deferred)

These `Config` fields are intentionally out of scope for this change:

- Secret env vars (`Config.EnvVar.secret` / `SecretValue`)
- `Config.system_prompt` (`skip_default`, `extra`)
- `Config.passthrough_credentials`

## New flags

All optional. When none are set, behavior is unchanged: `Sandbox.Config` stays
nil and a bare sandbox is created.

| Flag | Type | Maps to | Notes |
|---|---|---|---|
| `--env KEY=VALUE` | repeatable (StringArray) | `Config.env_vars[]` `{key, plain}` | Split on the **first** `=`. Empty key → error. Value may contain `=` and commas (StringArray does not comma-split). |
| `--git-repo url=...,branch=...,dir=...` | repeatable (StringArray) | `Config.git_repos[]` `{url, branch, target_dir}` | Comma-separated `key=value`. `url` required. `dir` maps to `target_dir`. Unknown key → error. SSH-safe (URL may contain `@`/`:`). |
| `--startup-command` | string | `Config.startup_command` | |
| `--session-init-command` | string | `Config.session_init_command` | |
| `--shutdown-policy` | string | `Config.shutdown_policy` | One of `ON_IDLE_NO_USER_ACTIONS`, `ON_IDLE`, `NEVER`. Resolved via enum value map as `SHUTDOWN_POLICY_<value>`. |
| `--integration-provider-id` | comma-sep / repeatable (StringSlice) | `Config.integration_provider_ids` | Simple ID tokens; commas are safe as a separator. |

### Flag type rationale

- `--env` and `--git-repo` use **StringArray** (each occurrence is one entry;
  no comma-splitting) because env values and git-repo specs legitimately
  contain commas. The git-repo helper does its own comma parsing.
- `--integration-provider-id` uses **StringSlice** (accepts both `a,b` and
  repeated flags) because provider IDs are simple tokens.

## Behavior & validation

1. **Config attached only when needed.** A `*sandboxv1.Sandbox_Config` is built
   and attached only if at least one config flag was explicitly set, detected
   via `cmd.Flags().Changed(...)`. Otherwise `Sandbox.Config` is left nil.

2. **`--template-id` is mutually exclusive with all custom config flags.** If
   `--template-id` is set together with any of the config flags above, return an
   error. This is required by the proto contract: when `source_template_id` is
   set on create, `config` MUST be empty — the server forks the template's
   config. The error names the conflict, e.g.:
   `cannot combine --template-id with config flags (--env, --git-repo, --startup-command, --session-init-command, --shutdown-policy, --integration-provider-id); the template's config is forked instead`.

3. **Clear field-level errors**, consistent with the existing `--type` /
   `--status` validation style:
   - `--env` without `=` → error.
   - `--env` with empty key → error.
   - `--git-repo` without a `url` key → error.
   - `--git-repo` with an unknown key → error (valid: `url`, `branch`, `dir`).
   - `--shutdown-policy` not in the allowed set → error listing valid values.

## Code shape

- New file `internal/cmd/sandbox/configflags.go` holds pure, testable helpers:
  - `parseEnvVar(s string) (*sandboxv1.Sandbox_Config_EnvVar, error)`
  - `parseGitRepo(s string) (*integrationv1.GitRepo, error)`
  - `parseShutdownPolicy(s string) (sandboxv1.Sandbox_Config_ShutdownPolicy, error)`
  - `buildConfig(cmd *cobra.Command, templateID string, env, gitRepos []string, startupCmd, sessionInitCmd, shutdownPolicy string, integrationIDs []string) (*sandboxv1.Sandbox_Config, error)`
    — assembles the `Config`, returns `(nil, nil)` when no config flag changed,
    and is the single place that enforces both field-level validation and the
    `--template-id` conflict. It takes `templateID` so the conflict
    (`templateID != ""` together with any config flag set) is detected and
    unit-tested here rather than in the cobra command.
- `newCreateCommand` in `command.go` wires the new flags and calls
  `buildConfig`, passing `templateID` through. This keeps the already-large
  `command.go` (~575 lines) from growing further with parsing logic.

## Testing (TDD)

`internal/cmd/sandbox/configflags_test.go`, written before wiring:

- `parseEnvVar`: `KEY=VALUE`; value containing `=` (split on first only);
  missing `=`; empty key.
- `parseGitRepo`: url only; url+branch+dir; SSH URL containing `@` and `:`;
  order-independent keys; missing url; unknown key; `dir`→`target_dir` mapping.
- `parseShutdownPolicy`: each valid value; invalid value.
- `buildConfig`: returns nil when nothing set; builds expected `Config` from a
  representative flag set; surfaces the `--template-id` + config-flag conflict
  error.

## Docs to keep in sync

- The `sandbox create` `Long` help text: new flags + usage examples.
- `internal/cmd/helpcmd/command.go` manifest entry for `retask sandbox create`:
  extend the `Flags` list with the six new flags (same discipline as the recent
  `help-llm` manifest-sync commit).
- Skill file `skills/retask-cli.md`: no change (it does not enumerate these
  per-command flags).

## Backward compatibility

Fully backward compatible. Existing invocations
(`--name`, `--type`, `--template-id`) behave exactly as before; the only new
failure mode is the explicit `--template-id` + config-flag conflict, which was
previously impossible to express.
