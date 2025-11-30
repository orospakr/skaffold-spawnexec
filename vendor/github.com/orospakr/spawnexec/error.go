package spawnexec

import (
	"errors"
	"os"
)

// Error is returned by LookPath when it fails to classify a file as an
// executable.
type Error struct {
	// Name is the file name for which the error occurred.
	Name string
	// Err is the underlying error.
	Err error
}

func (e *Error) Error() string {
	return "exec: " + e.Name + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error {
	return e.Err
}

// ExitError reports an unsuccessful exit by a command.
type ExitError struct {
	// ProcessState holds information about the exited process.
	*ProcessState

	// Stderr holds a subset of the standard error output from the
	// Cmd.Output method if standard error was not otherwise being
	// collected.
	//
	// If the error output is long, Stderr may contain only a prefix
	// and suffix of the output, with the middle replaced with
	// text about the number of omitted bytes.
	//
	// Stderr is provided for debugging, for inclusion in error messages.
	// Users with other needs should redirect Cmd.Stderr as needed.
	Stderr []byte
}

func (e *ExitError) Error() string {
	return e.ProcessState.String()
}

// Exited reports whether the program has exited.
// On Unix systems this reports true if the program exited due to calling exit,
// but false if the program terminated due to a signal.
func (e *ExitError) Exited() bool {
	return e.ProcessState.Exited()
}

// ExitCode returns the exit code of the exited process, or -1
// if the process hasn't exited or was terminated by a signal.
func (e *ExitError) ExitCode() int {
	return e.ProcessState.ExitCode()
}

// ErrNotFound is the error resulting if a path search failed to find an executable file.
var ErrNotFound = errors.New("executable file not found in $PATH")

// ErrDot indicates that a path lookup resolved to an executable
// in the current directory due to '.' being in the path, either
// implicitly or explicitly.
var ErrDot = errors.New("cannot run executable found relative to current directory")

// ErrWaitDelay is returned by (*Cmd).Wait if the process exits with a
// successful status code but its output pipes are not closed before the
// command's WaitDelay expires.
var ErrWaitDelay = errors.New("exec: WaitDelay expired before I/O complete")

// wrappedError wraps an error with a message prefix.
type wrappedError struct {
	prefix string
	err    error
}

func (w *wrappedError) Error() string {
	return w.prefix + w.err.Error()
}

func (w *wrappedError) Unwrap() error {
	return w.err
}

// wrapError wraps err with a message prefix.
func wrapError(prefix string, err error) error {
	if err == nil {
		return nil
	}
	return &wrappedError{prefix: prefix, err: err}
}

// isExecutable reports whether the file at path is executable.
func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().IsRegular() && fi.Mode()&0111 != 0
}
