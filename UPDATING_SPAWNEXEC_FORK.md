# Fork Maintenance: spawnexec Migration

This is a fork of [GoogleContainerTools/skaffold](https://github.com/GoogleContainerTools/skaffold)
that replaces all uses of `os/exec` with
[`github.com/orospakr/spawnexec`](https://github.com/orospakr/spawnexec).

## Why

On macOS 26+, Go's `os/exec` uses `fork+exec` to spawn subprocesses. A buggy
`pthread_atfork` handler in macOS's Network framework (`nw_settings_child_has_forked`
→ `NEFlowDirectorDestroy` → `_os_log_preferences_refresh`) causes the forked child
to spin at 100% CPU and never reach `exec()`. The child can only be killed with SIGKILL.

`spawnexec` is a drop-in replacement for `os/exec` that uses `posix_spawn` via cgo
on Darwin, bypassing fork entirely. On all other platforms it falls back to standard
`os/exec` behaviour, so the change is safe everywhere.

The fix is described in upstream issue:
https://github.com/GoogleContainerTools/skaffold/issues/9925

## What to do when merging a new upstream Skaffold release

### Step 0 — Merge and resolve conflicts

```bash
git fetch origin tag vX.Y.Z
git merge vX.Y.Z --no-commit
```

The `--no-commit` flag lets you inspect and fix conflicts before the merge is
recorded. Expect conflicts in two categories:

**`UD` conflicts (modify/delete):** upstream deleted a file that we had patched.
Since our only change was the import swap, always accept the deletion:

```bash
git rm <file>
```

These show up as `UD` in `git status`. The deleted files in v2.17→v2.19 included
`pkg/webhook/kubernetes/`, `vendor/github.com/sigstore/rekor/`, and
`vendor/github.com/skratchdot/open-golang/` — the pattern will differ each release.

**`UU` content conflicts:** almost always in import blocks. Two sub-patterns:

*Upstream added `"os/exec"` to a file we already patched:*
The conflict will show our side with nothing (we already replaced os/exec) and
upstream's side with `"os/exec"` plus possibly other new imports. Take any new
non-`os/exec` imports from upstream (e.g. `"sync"`, `"fmt"`), drop the `"os/exec"`
line, and leave our existing spawnexec import in place. Do NOT add a second spawnexec
import if one is already present — the build will catch it as "imported and not used".

*Upstream deleted an import we added:*
Take upstream's version; the file was already clean from upstream's perspective.

**`go.sum` conflict:** take upstream's version of the conflicting lines but preserve
the two `github.com/orospakr/spawnexec` lines that our fork adds.

After resolving all conflicts, run the audit below before committing.

### Step 1 — Find every remaining `os/exec` call site

```bash
# In Skaffold's own source (should always be zero — the original patch covered these):
grep -r '"os/exec"' . --include='*.go' -l | grep -v vendor/

# In vendored dependencies (the main concern after upstream updates):
grep -r '"os/exec"' vendor/ --include='*.go' -l | grep -v 'vendor/github.com/orospakr/spawnexec'
```

The vendor list will typically be long — 50+ files across many packages. That is
expected. Exclude Windows-only files and stdlib data files (which contain the string
`"os/exec"` as data, not as an import) from the list before patching:

```bash
grep -r '"os/exec"' vendor/ --include='*.go' -l \
  | grep -v 'vendor/github.com/orospakr/spawnexec' \
  | grep -v '_windows.go' \
  | grep -v 'golang.org/x/tools/internal/stdlib'
```

### Step 2 — Find every `execabs` call site

`golang.org/x/sys/execabs` is itself a thin wrapper around `os/exec`. Patch it at
the source (see below) so that all consumers of `execabs` are covered automatically.

```bash
# How many files use execabs as a dep (should only need the execabs package itself patched):
grep -r '"golang.org/x/sys/execabs"' vendor/ --include='*.go' -l
```

### Step 3 — Apply patches

#### 3a. Bulk-patch files that import `"os/exec"` directly

Use `perl -pi -e` for in-place replacement across all files at once — `sed -i` on
macOS BSD sed does not handle multi-file xargs pipelines reliably:

```bash
grep -r '"os/exec"' vendor/ --include='*.go' -l \
  | grep -v 'vendor/github.com/orospakr/spawnexec' \
  | grep -v '_windows.go' \
  | grep -v 'golang.org/x/tools/internal/stdlib' \
  | grep -v 'golang.org/x/sys/execabs' \
  | xargs perl -pi -e 's|"os/exec"|exec "github.com/orospakr/spawnexec"|g'
```

**After the bulk replace, check for two classes of collateral damage:**

**1. Files that used a non-`exec` alias** (e.g. `osexec "os/exec"`):
The substitution produces broken syntax like `osexec exec "github.com/..."`.
Fix by stripping the redundant `exec` keyword:

```bash
grep -r 'osexec exec "github.com/orospakr/spawnexec"' vendor/ --include='*.go' -l \
  | xargs perl -pi -e 's|osexec exec "github\.com/orospakr/spawnexec"|osexec "github.com/orospakr/spawnexec"|g'
```

If any other alias names appear (grep for `\w\+ exec "github.com/orospakr`), fix them
the same way.

**2. Comments that contained the string `"os/exec"`** (e.g. `// see "os/exec".Cmd.Run()`):
The substitution corrupts them into `// see exec "github.com/...".Cmd.Run()`.
Fix these manually — they are cosmetic and easy to spot with:

```bash
grep -r 'exec "github.com/orospakr/spawnexec"' vendor/ --include='*.go' | grep '//'
```

#### 3b. The `execabs` package (`vendor/golang.org/x/sys/execabs/`)

This package has three files. Patch all three to delegate to spawnexec rather than
`os/exec`. The bulk replace in step 3a handles the import swap; check the following
semantics manually:

**`execabs.go`** — the bulk replace handles it. The re-exported type aliases
(`Cmd`, `ExitError`, `Error`) and functions (`Command`, `CommandContext`, `LookPath`)
all exist in spawnexec under the same names.

**`execabs_go119.go`** — after the import swap, `isGo119ErrFieldSet` will fail to
compile because it references `cmd.Err`, a public field that exists on `os/exec.Cmd`
(Go 1.19+) but not on `spawnexec.Cmd`. Change the function body to return `false`:

```go
// spawnexec.Cmd has no Err field; fixCmd always applies its own check.
func isGo119ErrFieldSet(cmd *exec.Cmd) bool {
    return false
}
```

**`execabs_go118.go`** — the bulk replace handles it; function bodies are no-ops
that return false unconditionally and need no changes.

Once `execabs` itself is patched, every consumer of it (e.g. `go-git`'s
`vendor/github.com/go-git/go-git/v5/plumbing/transport/file/client.go`) is
automatically fixed without touching those files.

### Step 4 — Build and fix type incompatibilities

```bash
go build ./...
```

The build will surface any type incompatibilities that the mechanical import swap
could not handle. Fix them as they arise. Known categories encountered in Skaffold
v2.17 (new ones may appear as deps update):

**`*syscall.SysProcAttr` assigned to `cmd.SysProcAttr`**

`spawnexec.Cmd.SysProcAttr` is `*spawnexec.SysProcAttr`, not `*syscall.SysProcAttr`.
Change assignments of the form:

```go
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
```

to use spawnexec's type and map the fields across. `spawnexec.SysProcAttr` has:
`Setpgid`, `Setctty`, `Noctty`, `Ctty`, `Foreground`, `Pgid`. Fields with no
equivalent (e.g. `Setsid`, `Credential`, `Cloneflags`) must be dropped or the
surrounding code no-op'd with a comment explaining why.

Example — `vendor/github.com/docker/cli/cli/connhelper/commandconn/`:
- `commandconn.go`: `&syscall.SysProcAttr{}` → `&exec.SysProcAttr{}`
- `session_unix.go`: `cmd.SysProcAttr.Setsid = true` — no-op the function body
  (Setsid is not supported by spawnexec.SysProcAttr; SSH ProxyCommand session
  creation is skipped, which is acceptable for standard Kubernetes usage)

**`cmd.WaitDelay` type mismatch**

spawnexec declares `WaitDelay` as `int64` rather than `time.Duration`. Wrap any
assignment in an explicit cast:

```go
// before
cmd.WaitDelay = 30 * time.Second
// after
cmd.WaitDelay = int64(30 * time.Second)
```

Example: `vendor/golang.org/x/tools/internal/gocommand/invoke.go`.

### Step 5 — Final audit

```bash
# Zero results = clean (Windows-only and stdlib data files are acceptable exceptions)
grep -r '"os/exec"' . --include='*.go' \
  | grep -v 'vendor/github.com/orospakr/spawnexec' \
  | grep -v '_windows.go' \
  | grep -v 'golang.org/x/tools/internal/stdlib'

# Build must succeed
go build ./...
```

## Known files patched in this fork (as of Skaffold v2.19)

These were already updated by prior commits. After an upstream merge they may revert
if upstream touched them — check them first.

**Skaffold source** (all `os/exec` → spawnexec in one commit):
- `pkg/skaffold/util/cmd.go` and most of the surrounding call graph — see commit
  `798273421` for the full list.

**Vendor — auth/credential helpers** (high priority; fire on every k8s auth refresh):
- `vendor/k8s.io/client-go/plugin/pkg/client/auth/exec/exec.go` — k8s exec credential
  helper (runs `gke-gcloud-auth-plugin` etc.)
- `vendor/k8s.io/client-go/plugin/pkg/client/auth/exec/metrics.go` — same package,
  references `exec.ExitError`/`exec.Error`
- `vendor/cloud.google.com/go/auth/credentials/internal/externalaccount/executable_provider.go`
- `vendor/cloud.google.com/go/auth/internal/transport/cert/secureconnect_cert.go`
- `vendor/google.golang.org/api/internal/cert/secureconnect_cert.go`
- `vendor/golang.org/x/oauth2/google/externalaccount/executablecredsource.go`
- `vendor/github.com/google/go-containerregistry/pkg/v1/google/auth.go`

**Vendor — execabs** (patches go-git and any other execabs consumers transitively):
- `vendor/golang.org/x/sys/execabs/execabs.go`
- `vendor/golang.org/x/sys/execabs/execabs_go119.go` (needs `isGo119ErrFieldSet` no-op)
- `vendor/golang.org/x/sys/execabs/execabs_go118.go`

**Vendor — kustomize** (fires during manifest rendering):
- `vendor/sigs.k8s.io/kustomize/api/internal/git/gitrunner.go`
- `vendor/sigs.k8s.io/kustomize/api/internal/builtins/HelmChartInflationGenerator.go`
- `vendor/sigs.k8s.io/kustomize/api/internal/plugins/execplugin/execplugin.go`
- `vendor/sigs.k8s.io/kustomize/kyaml/fn/runtime/exec/exec.go`

**Vendor — Docker client**:
- `vendor/github.com/docker/docker-credential-helpers/client/command.go`
- `vendor/github.com/docker/cli/cli/config/credentials/default_store.go`
- `vendor/github.com/docker/cli/cli/connhelper/commandconn/commandconn.go`
  (also needs `&syscall.SysProcAttr{}` → `&exec.SysProcAttr{}`)
- `vendor/github.com/docker/cli/cli/connhelper/commandconn/session_unix.go`
  (needs `Setsid` no-op — see type incompatibilities above)

**Vendor — build tools** (ko, buildpacks, etc.):
- `vendor/github.com/google/ko/pkg/...` (multiple files)
- `vendor/github.com/buildpacks/lifecycle/...` (multiple files)
- `vendor/github.com/buildpacks/pack/...`

**Vendor — browser opener** (replaced `skratchdot/open-golang` in v2.19):
- `vendor/github.com/pkg/browser/browser.go`
- `vendor/github.com/pkg/browser/browser_linux.go`
- `vendor/github.com/pkg/browser/browser_darwin.go` (if present)
- `vendor/github.com/pkg/browser/browser_{openbsd,freebsd,netbsd}.go`

**Vendor — misc**:
- `vendor/golang.org/x/tools/internal/gocommand/invoke.go`
  (needs `WaitDelay` cast — see type incompatibilities above)
- `vendor/github.com/mitchellh/go-homedir/homedir.go`
- `vendor/github.com/joho/godotenv/godotenv.go`
- `vendor/github.com/Azure/go-autorest/autorest/azure/cli/token.go`
- `vendor/github.com/googleapis/enterprise-certificate-proxy/client/client.go`
- `vendor/github.com/moby/go-archive/compression/compression.go`
- `vendor/github.com/aws/aws-sdk-go-v2/credentials/processcreds/provider.go`
- `vendor/sigs.k8s.io/kind/pkg/exec/local.go` (uses `osexec` alias)
- `vendor/sigs.k8s.io/kind/pkg/cluster/internal/providers/nerdctl/provider.go` (uses `osexec` alias)
- `vendor/go.opentelemetry.io/otel/sdk/resource/host_id_exec.go`

## Gotchas

- **Windows-only files** (`_windows.go`, `//go:build windows`): leave them alone.
  They won't compile on Darwin anyway, and spawnexec's `spawn_other.go`
  (`//go:build !darwin`) already provides the fallback for Linux.

- **`golang.org/x/tools/internal/stdlib/`**: these files contain `"os/exec"` as
  string data in a map (a dependency graph), not as a Go import. Do not patch them.

- **Non-`exec` import aliases**: the bulk `perl` replace rewrites `"os/exec"` to
  `exec "github.com/..."` literally. If a file already had an alias (`osexec`, etc.),
  the result is syntactically broken (`osexec exec "..."`). Grep for this pattern and
  strip the redundant keyword after the bulk pass.

- **Comments containing `"os/exec"`**: the bulk replace hits these too, producing
  nonsense like `exec "github.com/orospakr/spawnexec".Cmd.Run()` in a comment.
  Fix them manually after the pass.

- **`spawnexec.SysProcAttr` is not `syscall.SysProcAttr`**: any code that assigns
  `&syscall.SysProcAttr{...}` to `cmd.SysProcAttr` will fail to compile. Translate
  to `&exec.SysProcAttr{...}` and drop fields that don't exist on spawnexec's type
  (`Setsid`, `Credential`, `Cloneflags`, etc.).

- **`cmd.WaitDelay` is `int64`, not `time.Duration`**: cast explicitly with `int64(...)`.

- **`cmd.Err` field**: exists on `os/exec.Cmd` (Go 1.19+) but not on `spawnexec.Cmd`.
  Any code reading this field should be changed to return/assume `false` or `nil`.

- **`cmd.ProcessState`**: spawnexec's `ProcessState` is its own type, not
  `*os.ProcessState`. If any code type-asserts to `*os.ProcessState`, that will
  break — handle case-by-case.
