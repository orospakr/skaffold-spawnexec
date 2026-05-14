# Skaffold Fork Hang Investigation Report

**Date**: 2025-11-28
**Go Version**: 1.25.4
**macOS Version**: Darwin 25.1.0
**Skaffold Version**: v2.17.0-dirty (built from source with debug symbols)

## Summary

Skaffold hangs on macOS when trying to execute `kustomize` as a subprocess. The hang occurs in the `fork()` system call's atfork child handlers, specifically in macOS's Network framework cleanup code. This manifests as a busy-wait loop consuming 100% CPU.

## Process Tree

```
40356 (parent)  - skaffold dev ... [State: Ss - sleeping, session leader]
└─ 40877 (child) - skaffold dev ... [State: R  - running, 100% CPU]
```

The child process is the forked copy waiting to `exec()` kustomize, but it's deadlocked before reaching `exec()`.

## Stack Trace (from lldb/delve)

### System-level Stack (lldb)
```
frame #0: libsystem_trace.dylib`_os_log_preferences_refresh + 56
frame #1: libsystem_trace.dylib`os_log_type_enabled + 768
frame #2: libnetworkextension.dylib`NEFlowDirectorDestroy + 64
frame #3: Network`nw_path_release_globals + 164
frame #4: Network`nw_settings_child_has_forked() + 332
frame #5: libsystem_pthread.dylib`_pthread_atfork_child_handlers + 76
frame #6: libsystem_c.dylib`fork + 112
frame #7: skaffold`runtime.syscall.abi0 + 44
```

### Go-level Stack (delve)
```
Frame 0:  _os_log_preferences_refresh (system deadlock)
Frame 4:  syscall.forkAndExecInChild at exec_libc2.go:86
Frame 5:  syscall.forkExec at exec_unix.go:208
Frame 9:  os/exec.(*Cmd).Start at exec.go:725
Frame 10: util.(*Commander).RunCmdOut at cmd.go:102
Frame 12: kustomize.Kustomize.render at kustomize.go:143
```

### Command Being Executed
```go
*os/exec.Cmd {
    Path: "/opt/homebrew/bin/kustomize",
    Args: ["kustomize", "build", "/Users/andrew/Developer/rover/projects/seatgeek-v3/bobcat/infra/..."],
}
```

## Root Cause Analysis

### 1. Go's Fork Implementation (Go 1.25.4)

**File**: `syscall/exec_libc2.go:86`
```go
r1, _, err1 = rawSyscall(abi.FuncPCABI0(libc_fork_trampoline), 0, 0, 0)
```

Go 1.25.4 is using **`libc_fork`** which includes macOS atfork handlers, NOT the direct `__fork` syscall.

### 2. Workaround Exists But Is Incomplete

**File**: `runtime/sys_darwin.go:213-252`

Go has an `osinit_hack()` workaround that "warms up" some atfork handlers:
- Calls `notify_is_valid_token(0)` to initialize notify globals
- Calls `xpc_date_create_from_current()` to initialize xpc state

**However**, this workaround does NOT cover:
- Network framework (`nw_settings_child_has_forked()`)
- Logging system (`_os_log_preferences_refresh`)

### 3. The Deadlock Mechanism

1. Parent skaffold calls `fork()` to spawn kustomize
2. Child process (exact copy of parent) is created
3. macOS runs atfork child handlers to clean up state
4. Network framework handler tries to access logging system
5. Logging system tries to refresh preferences
6. **Deadlock**: Preferences lock was held by parent, child can't acquire it
7. Result: Infinite busy-wait loop at 100% CPU

## Why This Happens

When `fork()` is called in a multi-threaded program with network connections open:
1. Only one thread (the calling thread) survives in the child
2. Other threads may have been holding locks
3. Atfork handlers try to clean up/reinitialize
4. If those handlers need locks that were held by now-dead threads → deadlock

## Historical Context

### Related Go Issues

1. **[golang/go#56784](https://github.com/golang/go/issues/56784)** - os/exec: tests hang on macOS due to Apple libc fork bugs
   - Original report of fork hangs in macOS 12.6.1
   - 50% reproduction rate in tests
   - Hangs in `libsystem_notify.dylib` and `xpc_atfork_child`

2. **[golang/go#56837](https://github.com/golang/go/issues/56837)** - Go 1.19 backport of fix

3. **[golang/go#33565](https://github.com/golang/go/issues/33565)** - Earlier macOS fork hang issue

4. **[golang/go#57263](https://github.com/golang/go/issues/57263)** - xpc_date_create_from_current workaround added

### Proposed Fix (Not Implemented in Go 1.25.4)

According to [golang-checkins](https://groups.google.com/g/golang-checkins/c/WNlxBbT3WTs), the fix was to:
> "Call __fork instead of fork on darwin to avoid the libc atfork and atexit handlers"

**But**: Go 1.25.4 still uses `libc_fork`, not `__fork`.

### Apple's Position

From [Apple Developer Forums](https://developer.apple.com/forums/thread/737464):
> "Combining fork with Apple's frameworks is tricky - if you only use Posix APIs, fork should behave reliably, but once you start using higher-level frameworks, they can't make any guarantees."

Recommended solution: Use `posix_spawn` which combines fork+exec atomically.

## Why "Normal" Skaffold Usage Triggers This

This isn't a "child skaffold spawning" issue - it's the standard fork+exec pattern:

1. Parent skaffold needs to run kustomize
2. Calls `fork()` to create child process
3. Child process is exact copy (same command line: `skaffold dev ...`)
4. Child SHOULD call `exec()` to replace itself with kustomize
5. BUT child deadlocks in fork's atfork handlers BEFORE reaching exec

The child appearing with identical args is **normal** - that's how fork works. The child is just the forked copy that never made it to exec.

## Impact

- **Frequency**: Intermittent (depends on timing of which locks are held during fork)
- **Symptom**: Skaffold hangs indefinitely, 100% CPU on child process
- **Workaround**: Kill process, retry (may work next time due to timing)
- **Root Cause**: macOS Network framework not covered by Go's atfork workaround

## Reproduction

**Environment**:
- macOS 15.1 (Darwin 25.1.0)
- Go 1.25.4
- Skaffold v2.17.0
- Multi-module project with kustomize renderer

**Trigger**:
```bash
skaffold dev --force-colors --build-concurrency=0 \
  -m bobcat-app,bobcat-server \
  -p dev -p dev-seatgeek -p dev-fan-profile -p dev-engage
```

**Result**: Hangs when attempting to render kustomize manifests

## Recommendations

### Short-term Workarounds

1. **Kill and retry**: `kill -9 <PID>`
2. **Use Linux**: Fork works reliably on Linux
3. **Use Docker Desktop**: Run skaffold in Linux container

### Long-term Solutions

1. **Report to Go Team**: New manifestation of #56784 with Network framework
2. **Implement __fork**: Go should use direct `__fork` syscall on Darwin
3. **Use posix_spawn**: Alternative to fork+exec that's more macOS-friendly
4. **Expand osinit_hack**: Add Network framework initialization

## Related Files

**Go 1.25.4 Source**:
- `syscall/exec_libc2.go:86` - Fork call using libc
- `runtime/sys_darwin.go:213-252` - osinit_hack workaround
- `syscall/zsyscall_darwin_arm64.go:1774` - libc_fork_trampoline definition

**Skaffold Source**:
- `pkg/skaffold/render/renderer/kustomize/kustomize.go:143` - Kustomize execution
- `pkg/skaffold/util/cmd.go:102` - Command execution wrapper

## Investigation Tools Used

- `ps`, `htop` - Process monitoring
- `lsof` - Open file descriptors
- `lldb` - System-level debugging
- `dlv` (delve) - Go-aware debugging
- `sample` - CPU profiling on macOS

## Key Commands

```bash
# Check process tree
ps -p <PID> -o pid,ppid,pgid,stat,time,rss,command

# Get stack trace
lldb -p <PID>
(lldb) thread backtrace all

# Go-level debugging
dlv attach <PID>
(dlv) goroutines
(dlv) goroutine 1 bt
(dlv) frame <N>
(dlv) print <var>
```

## Conclusion

This is a **Go runtime bug** where the `osinit_hack` workaround for macOS fork hangs is incomplete. The workaround covers `notify` and `xpc` subsystems but not the Network framework or logging system.

The proper fix (using `__fork` to bypass all atfork handlers) has NOT been fully implemented in Go 1.25.4, despite references in the issue tracker suggesting it was.

This should be reported to the Go team as a NEW manifestation of golang/go#56784 with:
- Specific stack trace showing Network framework deadlock
- Go version 1.25.4
- macOS 15.1
- Reproduction case with Skaffold + kustomize

---

**References**:
- https://github.com/golang/go/issues/56784
- https://github.com/golang/go/issues/56837
- https://github.com/golang/go/issues/57263
- https://groups.google.com/g/golang-checkins/c/WNlxBbT3WTs
- https://developer.apple.com/forums/thread/737464
