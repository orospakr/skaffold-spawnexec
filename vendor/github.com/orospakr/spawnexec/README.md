# spawnexec

A drop-in replacement for Go's `os/exec` package that uses `posix_spawn` on macOS instead of the traditional `fork`+`exec` pattern.

## Why?

macOS system frameworks (CoreFoundation, Security, etc.) register `atfork` handlers that can cause issues when a large Go process forks:

- **Deadlocks and busy-wait spins** in system framework code during fork
- **Performance overhead** from copying page tables and running atfork handlers
- **Memory pressure** from copy-on-write overhead in large processes

Apple's recommended approach is to use `posix_spawn` instead of `fork`+`exec`. The `posix_spawn` API creates a new process directly without the intermediate fork step, avoiding these issues entirely.

This package provides the familiar `os/exec` API while using `posix_spawn` under the hood on macOS.

## Installation

```bash
go get github.com/spawnexec/spawnexec
```

## Usage

The API mirrors `os/exec` exactly. Simply replace your imports:

```go
// Before
import "os/exec"

cmd := exec.Command("echo", "hello")

// After
import "github.com/spawnexec/spawnexec"

cmd := spawnexec.Command("echo", "hello")
```

### Basic Examples

```go
package main

import (
    "fmt"
    "github.com/spawnexec/spawnexec"
)

func main() {
    // Simple command execution
    cmd := spawnexec.Command("echo", "hello", "world")
    if err := cmd.Run(); err != nil {
        panic(err)
    }

    // Capture output
    out, err := spawnexec.Command("date").Output()
    if err != nil {
        panic(err)
    }
    fmt.Println(string(out))

    // Capture stdout and stderr together
    out, err = spawnexec.Command("sh", "-c", "echo out; echo err >&2").CombinedOutput()
    if err != nil {
        panic(err)
    }
    fmt.Println(string(out))
}
```

### Environment and Working Directory

```go
cmd := spawnexec.Command("printenv", "MY_VAR")
cmd.Env = append(os.Environ(), "MY_VAR=hello")
cmd.Dir = "/tmp"
out, _ := cmd.Output()
```

### Pipes

```go
cmd := spawnexec.Command("cat")
stdin, _ := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()

cmd.Start()

go func() {
    io.WriteString(stdin, "hello from pipe")
    stdin.Close()
}()

out, _ := io.ReadAll(stdout)
cmd.Wait()
```

### Context Support

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

cmd := spawnexec.CommandContext(ctx, "sleep", "60")
err := cmd.Run() // Will be killed after 5 seconds
```

## Platform Support

| Platform             | Implementation          |
| -------------------- | ----------------------- |
| macOS (Darwin)       | `posix_spawn` via cgo   |
| Linux, Windows, etc. | Falls back to `os/exec` |

On non-Darwin platforms, the package transparently wraps `os/exec`, so your code remains portable.

## Requirements

- **macOS 10.15+** for `Dir` support (uses `posix_spawn_file_actions_addchdir_np`)
- Go 1.18+

## Performance

Benchmarks on Apple M2 Pro show `spawnexec` is **35-42% faster** than `os/exec`:

| Operation | spawnexec | os/exec | Improvement |
| --------- | --------- | ------- | ----------- |
| Run(true) | 1.24ms    | 1.90ms  | 35% faster  |
| Run(echo) | 1.28ms    | 1.95ms  | 35% faster  |
| Output    | 1.26ms    | 2.16ms  | 42% faster  |
| WithStdin | 1.38ms    | 2.17ms  | 37% faster  |

Run benchmarks yourself:

```bash
go test -bench=. -benchmem ./...
```

## API Reference

The following `os/exec` APIs are supported:

- `Command(name string, arg ...string) *Cmd`
- `CommandContext(ctx context.Context, name string, arg ...string) *Cmd`
- `LookPath(file string) (string, error)`
- `(*Cmd).Run() error`
- `(*Cmd).Start() error`
- `(*Cmd).Wait() error`
- `(*Cmd).Output() ([]byte, error)`
- `(*Cmd).CombinedOutput() ([]byte, error)`
- `(*Cmd).StdinPipe() (io.WriteCloser, error)`
- `(*Cmd).StdoutPipe() (io.ReadCloser, error)`
- `(*Cmd).StderrPipe() (io.ReadCloser, error)`

Supported `Cmd` fields:

- `Path`, `Args`, `Env`, `Dir`
- `Stdin`, `Stdout`, `Stderr`
- `ExtraFiles`
- `SysProcAttr` (partial: `Setpgid`, `Pgid`)
- `Process`, `ProcessState`

## Caveats

- **cgo required** on Darwin (uses C wrapper for `posix_spawn`)
- **macOS 10.15+** required for `Dir` field support
- Some advanced `SysProcAttr` options are not yet implemented

## License

MIT
