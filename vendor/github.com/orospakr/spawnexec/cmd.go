// Golang os/exec-compatible package that uses posix_spawn on macOS instead of fork+exec,
// to avoid atfork bugs in system frameworks and improve performance.
//
// macOS as a platform clearly prefers the use of posix_spawn instead of the classic
// UNIX fork exec pattern.
package spawnexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Cmd represents an external command being prepared or run.
//
// A Cmd cannot be reused after calling its Run, Output or CombinedOutput
// methods.
type Cmd struct {
	// Path is the path of the command to run.
	//
	// This is the only field that must be set to a non-zero
	// value. If Path is relative, it is evaluated relative
	// to Dir.
	Path string

	// Args holds command line arguments, including the command as Args[0].
	// If the Args field is empty or nil, Run uses {Path}.
	//
	// In typical use, both Path and Args are set by calling Command.
	Args []string

	// Env specifies the environment of the process.
	// Each entry is of the form "key=value".
	// If Env is nil, the new process uses the current process's
	// environment.
	// If Env contains duplicate environment keys, only the last
	// value in the slice for each duplicate key is used.
	Env []string

	// Dir specifies the working directory of the command.
	// If Dir is the empty string, Run runs the command in the
	// calling process's current directory.
	//
	// Note: On macOS, posix_spawn does not natively support setting
	// the working directory. This implementation uses posix_spawn_file_actions_addchdir_np
	// which is available on macOS 10.15+.
	Dir string

	// Stdin specifies the process's standard input.
	//
	// If Stdin is nil, the process reads from the null device (os.DevNull).
	//
	// If Stdin is an *os.File, the process's standard input is connected
	// directly to that file.
	//
	// Otherwise, during the execution of the command a separate
	// goroutine reads from Stdin and delivers that data to the command
	// over a pipe. In this case, Wait does not complete until the
	// goroutine stops copying, either because it has reached the end of
	// Stdin (EOF or a read error), or because writing to the pipe returned
	// an error, or because a nonzero WaitDelay was set and expired.
	Stdin io.Reader

	// Stdout and Stderr specify the process's standard output and error.
	//
	// If either is nil, Run connects the corresponding file descriptor
	// to the null device (os.DevNull).
	//
	// If either is an *os.File, the corresponding output from the process
	// is connected directly to that file.
	//
	// Otherwise, during the execution of the command a separate goroutine
	// reads from the process over a pipe and delivers that data to the
	// corresponding Writer. In this case, Wait does not complete until the
	// goroutine reaches EOF or encounters an error or a nonzero WaitDelay
	// expires.
	//
	// If Stdout and Stderr are the same writer, and have a type that can
	// be compared with ==, at most one goroutine at a time will call Write.
	Stdout io.Writer
	Stderr io.Writer

	// ExtraFiles specifies additional open files to be inherited by the
	// new process. It does not include standard input, standard output, or
	// standard error. If non-nil, entry i becomes file descriptor 3+i.
	ExtraFiles []*os.File

	// SysProcAttr holds optional, operating system-specific attributes.
	// Currently not fully supported in spawnexec.
	SysProcAttr *SysProcAttr

	// Cancel is called when the context passed to CommandContext is canceled.
	// By default, Cancel calls the Kill method on the Process.
	Cancel func() error

	// WaitDelay is the amount of time to wait for the process to finish
	// after the context is done and Cancel has been called.
	// Not yet implemented in spawnexec.
	WaitDelay int64

	// Process is the underlying process, once started.
	Process *Process

	// ProcessState contains information about an exited process.
	// If the process was started successfully, Wait or Run will
	// populate its ProcessState when the command completes.
	ProcessState *ProcessState

	// ctx is the context passed to CommandContext
	ctx       context.Context
	ctxCancel context.CancelFunc

	// Internal state
	lookPathErr    error // LookPath error, if any
	finished       bool  // true after Wait returns
	childIOFiles   []*os.File
	parentIOPipes  []*os.File
	goroutine      []func() error
	goroutineErr   []error
	goroutineMu    sync.Mutex
	stdinPipeUsed  bool
	stdoutPipeUsed bool
	stderrPipeUsed bool

	// osCmd is used on non-darwin platforms to hold the underlying os/exec.Cmd
	osCmd interface{}
}

// SysProcAttr holds optional, operating system-specific attributes.
type SysProcAttr struct {
	// Setpgid sets the process group ID of the child to Pgid,
	// or, if Pgid == 0, to the new child's process ID.
	Setpgid bool

	// Setctty sets the controlling terminal of the child to
	// file descriptor Ctty. Ctty must be a terminal file descriptor
	// in the child process.
	Setctty bool

	// Noctty makes the child process not have a controlling terminal.
	Noctty bool

	// Ctty is the controlling terminal file descriptor.
	Ctty int

	// Foreground places the child process group in the foreground.
	Foreground bool

	// Pgid is the process group ID.
	Pgid int
}

// Command returns the Cmd struct to execute the named program with
// the given arguments.
//
// It sets only the Path and Args in the returned structure.
//
// If name contains no path separators, Command uses LookPath to
// resolve name to a complete path if possible. Otherwise it uses name
// directly as Path.
//
// The returned Cmd's Args field is constructed from the command name
// followed by the elements of arg, so arg should not include the
// command name itself. For example, Command("echo", "hello").
// Args[0] is always name, not the possibly resolved Path.
func Command(name string, arg ...string) *Cmd {
	cmd := &Cmd{
		Path: name,
		Args: append([]string{name}, arg...),
	}
	if filepath.Base(name) == name {
		lp, err := LookPath(name)
		if err != nil {
			cmd.lookPathErr = err
		} else {
			cmd.Path = lp
		}
	}
	return cmd
}

// CommandContext is like Command but includes a context.
//
// The provided context is used to interrupt the process
// (by calling cmd.Cancel or os.Process.Kill)
// if the context becomes done before the command completes on its own.
//
// CommandContext sets the command's Cancel function to invoke the Kill method
// on its Process, and leaves its WaitDelay unset.
func CommandContext(ctx context.Context, name string, arg ...string) *Cmd {
	if ctx == nil {
		panic("nil Context")
	}
	cmd := Command(name, arg...)
	cmd.ctx = ctx
	return cmd
}

// String returns a human-readable description of c.
// It is intended only for debugging.
// In particular, it is not suitable for use as input to a shell.
// The output of String may vary across Go releases.
func (c *Cmd) String() string {
	if c.lookPathErr != nil {
		return strings.Join(c.Args, " ")
	}
	var b strings.Builder
	b.WriteString(c.Path)
	for _, a := range c.Args[1:] {
		b.WriteByte(' ')
		b.WriteString(a)
	}
	return b.String()
}

// Run starts the specified command and waits for it to complete.
//
// The returned error is nil if the command runs, has no problems
// copying stdin, stdout, and stderr, and exits with a zero exit status.
//
// If the command starts but does not complete successfully, the error is of
// type *ExitError. Other error types may be returned for other situations.
//
// If the calling goroutine has locked the operating system thread
// with runtime.LockOSThread and modified any inheritable OS-level
// thread state (for example, Linux or Plan 9 name spaces), the new
// process will inherit the caller's thread state.
func (c *Cmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

// Output runs the command and returns its standard output.
// Any returned error will usually be of type *ExitError.
// If c.Stderr was nil, Output populates ExitError.Stderr.
func (c *Cmd) Output() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var stdout bytes.Buffer
	c.Stdout = &stdout

	captureErr := c.Stderr == nil
	if captureErr {
		c.Stderr = &prefixSuffixSaver{N: 32 << 10}
	}

	err := c.Run()
	if err != nil {
		if ee, ok := err.(*ExitError); ok && captureErr {
			ee.Stderr = c.Stderr.(*prefixSuffixSaver).Bytes()
		}
	}
	return stdout.Bytes(), err
}

// CombinedOutput runs the command and returns its combined standard
// output and standard error.
func (c *Cmd) CombinedOutput() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if c.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	var b bytes.Buffer
	c.Stdout = &b
	c.Stderr = &b
	err := c.Run()
	return b.Bytes(), err
}

// StdinPipe returns a pipe that will be connected to the command's
// standard input when the command starts.
// The pipe will be closed automatically after Wait sees the command exit.
// A caller need only call Close to force the pipe to close sooner.
// For example, if the command being run will not exit until standard input
// is closed, the caller must close the pipe.
func (c *Cmd) StdinPipe() (io.WriteCloser, error) {
	if c.Stdin != nil {
		return nil, errors.New("exec: Stdin already set")
	}
	if c.Process != nil {
		return nil, errors.New("exec: StdinPipe after process started")
	}
	if c.stdinPipeUsed {
		return nil, errors.New("exec: StdinPipe already called")
	}
	c.stdinPipeUsed = true

	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c.Stdin = pr
	c.childIOFiles = append(c.childIOFiles, pr)
	c.parentIOPipes = append(c.parentIOPipes, pw)
	return pw, nil
}

// StdoutPipe returns a pipe that will be connected to the command's
// standard output when the command starts.
//
// Wait will close the pipe after seeing the command exit, so most callers
// need not close the pipe themselves. It is thus incorrect to call Wait
// before all reads from the pipe have completed.
// For the same reason, it is incorrect to call Run when using StdoutPipe.
// See the example for idiomatic usage.
func (c *Cmd) StdoutPipe() (io.ReadCloser, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if c.Process != nil {
		return nil, errors.New("exec: StdoutPipe after process started")
	}
	if c.stdoutPipeUsed {
		return nil, errors.New("exec: StdoutPipe already called")
	}
	c.stdoutPipeUsed = true

	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c.Stdout = pw
	c.childIOFiles = append(c.childIOFiles, pw)
	c.parentIOPipes = append(c.parentIOPipes, pr)
	return pr, nil
}

// StderrPipe returns a pipe that will be connected to the command's
// standard error when the command starts.
//
// Wait will close the pipe after seeing the command exit, so most callers
// need not close the pipe themselves. It is thus incorrect to call Wait
// before all reads from the pipe have completed.
// For the same reason, it is incorrect to use Run when using StderrPipe.
// See the StdoutPipe example for idiomatic usage.
func (c *Cmd) StderrPipe() (io.ReadCloser, error) {
	if c.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	if c.Process != nil {
		return nil, errors.New("exec: StderrPipe after process started")
	}
	if c.stderrPipeUsed {
		return nil, errors.New("exec: StderrPipe already called")
	}
	c.stderrPipeUsed = true

	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c.Stderr = pw
	c.childIOFiles = append(c.childIOFiles, pw)
	c.parentIOPipes = append(c.parentIOPipes, pr)
	return pr, nil
}

// Environ returns a copy of the environment in which the command would be run
// as it is currently configured.
func (c *Cmd) Environ() []string {
	env := c.Env
	if env == nil {
		env = os.Environ()
	}
	return env
}

// prefixSuffixSaver is an io.Writer which retains the first N bytes
// and the last N bytes written to it. The Bytes() method reconstructs
// it with a pretty error message.
type prefixSuffixSaver struct {
	N         int // max bytes of prefix and suffix
	prefix    []byte
	suffix    []byte // ring buffer once len googlet googletN
	suffixOff int    // offset to write into suffix
	skipped   int64
}

func (w *prefixSuffixSaver) Write(p []byte) (n int, err error) {
	lenp := len(p)
	p = w.fill(&w.prefix, p)

	// Only keep the last N bytes of suffix data.
	if overage := len(p) - w.N; overage > 0 {
		p = p[overage:]
		w.skipped += int64(overage)
	}
	p = w.fill(&w.suffix, p)

	for len(p) > 0 {
		n := copy(w.suffix[w.suffixOff:], p)
		p = p[n:]
		w.suffixOff += n
		if w.suffixOff == w.N {
			w.suffixOff = 0
		}
	}
	return lenp, nil
}

func (w *prefixSuffixSaver) fill(dst *[]byte, p []byte) (pRemaining []byte) {
	if remain := w.N - len(*dst); remain > 0 {
		add := min(len(p), remain)
		*dst = append(*dst, p[:add]...)
		p = p[add:]
	}
	return p
}

func (w *prefixSuffixSaver) Bytes() []byte {
	if w.suffix == nil {
		return w.prefix
	}
	if w.skipped == 0 {
		return append(w.prefix, w.suffix...)
	}
	var buf bytes.Buffer
	buf.Grow(len(w.prefix) + len(w.suffix) + 50)
	buf.Write(w.prefix)
	buf.WriteString("\n... omitting ")
	buf.WriteString(itoa(int(w.skipped)))
	buf.WriteString(" bytes ...\n")
	buf.Write(w.suffix[w.suffixOff:])
	buf.Write(w.suffix[:w.suffixOff])
	return buf.Bytes()
}

func itoa(i int) string {
	var b [20]byte
	bp := len(b)
	for i > 0 {
		bp--
		b[bp] = byte('0' + i%10)
		i /= 10
	}
	return string(b[bp:])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
