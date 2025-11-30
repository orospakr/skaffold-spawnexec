//go:build darwin

package spawnexec

/*
#include <spawn.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <signal.h>
#include <unistd.h>
#include <fcntl.h>

// posix_spawn_file_actions helpers
int init_file_actions(posix_spawn_file_actions_t *actions) {
    return posix_spawn_file_actions_init(actions);
}

int destroy_file_actions(posix_spawn_file_actions_t *actions) {
    return posix_spawn_file_actions_destroy(actions);
}

int add_close_action(posix_spawn_file_actions_t *actions, int fd) {
    return posix_spawn_file_actions_addclose(actions, fd);
}

int add_dup2_action(posix_spawn_file_actions_t *actions, int fd, int newfd) {
    return posix_spawn_file_actions_adddup2(actions, fd, newfd);
}

int add_open_action(posix_spawn_file_actions_t *actions, int fd, const char *path, int oflag, mode_t mode) {
    return posix_spawn_file_actions_addopen(actions, fd, path, oflag, mode);
}

// macOS 10.15+ supports chdir in file actions via posix_spawn_file_actions_addchdir_np
// macOS 26+ deprecates the _np version in favor of posix_spawn_file_actions_addchdir
#if defined(__APPLE__) && defined(__MACH__)

// Try to use the standard function first (macOS 26+), fall back to _np version
extern int posix_spawn_file_actions_addchdir(posix_spawn_file_actions_t *file_actions, const char *path) __attribute__((weak_import));
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
extern int posix_spawn_file_actions_addchdir_np(posix_spawn_file_actions_t *file_actions, const char *path) __attribute__((weak_import));
#pragma clang diagnostic pop

int add_chdir_action(posix_spawn_file_actions_t *actions, const char *path) {
    // Try standard function first (macOS 26+)
    if (posix_spawn_file_actions_addchdir != NULL) {
        return posix_spawn_file_actions_addchdir(actions, path);
    }
    // Fall back to _np version (macOS 10.15-25)
    #pragma clang diagnostic push
    #pragma clang diagnostic ignored "-Wdeprecated-declarations"
    if (posix_spawn_file_actions_addchdir_np != NULL) {
        return posix_spawn_file_actions_addchdir_np(actions, path);
    }
    #pragma clang diagnostic pop
    // Return error if neither available
    return ENOSYS;
}

int has_chdir_np() {
    if (posix_spawn_file_actions_addchdir != NULL) {
        return 1;
    }
    #pragma clang diagnostic push
    #pragma clang diagnostic ignored "-Wdeprecated-declarations"
    int result = posix_spawn_file_actions_addchdir_np != NULL ? 1 : 0;
    #pragma clang diagnostic pop
    return result;
}
#else
int add_chdir_action(posix_spawn_file_actions_t *actions, const char *path) {
    return ENOSYS;
}
int has_chdir_np() {
    return 0;
}
#endif

// posix_spawnattr helpers
int init_spawnattr(posix_spawnattr_t *attr) {
    return posix_spawnattr_init(attr);
}

int destroy_spawnattr(posix_spawnattr_t *attr) {
    return posix_spawnattr_destroy(attr);
}

int set_spawnattr_flags(posix_spawnattr_t *attr, short flags) {
    return posix_spawnattr_setflags(attr, flags);
}

int set_spawnattr_pgroup(posix_spawnattr_t *attr, pid_t pgroup) {
    return posix_spawnattr_setpgroup(attr, pgroup);
}

int set_spawnattr_sigdefault(posix_spawnattr_t *attr, sigset_t *sigdefault) {
    return posix_spawnattr_setsigdefault(attr, sigdefault);
}

int set_spawnattr_sigmask(posix_spawnattr_t *attr, sigset_t *sigmask) {
    return posix_spawnattr_setsigmask(attr, sigmask);
}

// Spawn wrapper
int do_posix_spawn(pid_t *pid, const char *path,
                   posix_spawn_file_actions_t *file_actions,
                   posix_spawnattr_t *attrp,
                   char *const argv[], char *const envp[]) {
    return posix_spawn(pid, path, file_actions, attrp, argv, envp);
}

// Helper to get /dev/null path
const char* devnull_path() {
    return "/dev/null";
}

// Signal set helpers
void sigset_empty(sigset_t *set) {
    sigemptyset(set);
}

void sigset_fill(sigset_t *set) {
    sigfillset(set);
}

void sigset_add(sigset_t *set, int signum) {
    sigaddset(set, signum);
}
*/
import "C"
import (
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// spawn flags constants
const (
	_POSIX_SPAWN_RESETIDS        = C.POSIX_SPAWN_RESETIDS
	_POSIX_SPAWN_SETPGROUP       = C.POSIX_SPAWN_SETPGROUP
	_POSIX_SPAWN_SETSIGDEF       = C.POSIX_SPAWN_SETSIGDEF
	_POSIX_SPAWN_SETSIGMASK      = C.POSIX_SPAWN_SETSIGMASK
	_POSIX_SPAWN_SETEXEC         = 0x0040 // macOS specific
	_POSIX_SPAWN_START_SUSPENDED = 0x0080 // macOS specific
	_POSIX_SPAWN_CLOEXEC_DEFAULT = 0x4000 // macOS specific
)

// hasChdir reports whether posix_spawn_file_actions_addchdir_np is available.
func hasChdir() bool {
	return C.has_chdir_np() != 0
}

// Start starts the specified command but does not wait for it to complete.
//
// If Start returns successfully, the c.Process field will be set.
//
// After a successful call to Start the Wait method must be called in
// order to release associated system resources.
func (c *Cmd) Start() error {
	if c.lookPathErr != nil {
		return c.lookPathErr
	}
	if c.Process != nil {
		return errors.New("exec: already started")
	}
	if c.finished {
		return errors.New("exec: already finished")
	}

	// Check if context is already done
	if c.ctx != nil {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		default:
		}
	}

	// Resolve path
	path := c.Path
	if c.Dir != "" && !isAbs(path) {
		path = joinPath(c.Dir, path)
	}

	// Setup environment
	env := c.Env
	if env == nil {
		env = os.Environ()
	}

	// Setup file actions for I/O redirection
	var fileActions C.posix_spawn_file_actions_t
	if ret := C.init_file_actions(&fileActions); ret != 0 {
		return syscall.Errno(ret)
	}
	defer C.destroy_file_actions(&fileActions)

	// Track file descriptors to close in parent after spawn
	var closeAfterSpawn []int
	var closersToClose []io.Closer

	// Setup stdin
	stdinFd, stdinCloser, err := c.setupStdin(&fileActions)
	if err != nil {
		return wrapError("exec: ", err)
	}
	if stdinCloser != nil {
		closersToClose = append(closersToClose, stdinCloser)
	}
	if stdinFd >= 0 {
		closeAfterSpawn = append(closeAfterSpawn, stdinFd)
	}

	// Setup stdout
	stdoutFd, stdoutCloser, err := c.setupStdout(&fileActions)
	if err != nil {
		closeClosers(closersToClose)
		return wrapError("exec: ", err)
	}
	if stdoutCloser != nil {
		closersToClose = append(closersToClose, stdoutCloser)
	}
	if stdoutFd >= 0 {
		closeAfterSpawn = append(closeAfterSpawn, stdoutFd)
	}

	// Setup stderr
	stderrFd, stderrCloser, err := c.setupStderr(&fileActions)
	if err != nil {
		closeClosers(closersToClose)
		return wrapError("exec: ", err)
	}
	if stderrCloser != nil {
		closersToClose = append(closersToClose, stderrCloser)
	}
	if stderrFd >= 0 {
		closeAfterSpawn = append(closeAfterSpawn, stderrFd)
	}

	// Setup extra files
	for i, f := range c.ExtraFiles {
		if f != nil {
			fd := int(f.Fd())
			targetFd := 3 + i
			if ret := C.add_dup2_action(&fileActions, C.int(fd), C.int(targetFd)); ret != 0 {
				closeClosers(closersToClose)
				return syscall.Errno(ret)
			}
		}
	}

	// Setup working directory if specified
	if c.Dir != "" {
		if !hasChdir() {
			closeClosers(closersToClose)
			return errors.New("exec: setting Dir requires macOS 10.15+")
		}
		cDir := C.CString(c.Dir)
		defer C.free(unsafe.Pointer(cDir))
		if ret := C.add_chdir_action(&fileActions, cDir); ret != 0 {
			closeClosers(closersToClose)
			return syscall.Errno(ret)
		}
	}

	// Setup spawn attributes
	var attr C.posix_spawnattr_t
	if ret := C.init_spawnattr(&attr); ret != 0 {
		closeClosers(closersToClose)
		return syscall.Errno(ret)
	}
	defer C.destroy_spawnattr(&attr)

	// Set flags for CLOEXEC_DEFAULT to avoid leaking fds
	var flags C.short = _POSIX_SPAWN_CLOEXEC_DEFAULT

	// Reset signals to default in child
	flags |= _POSIX_SPAWN_SETSIGDEF | _POSIX_SPAWN_SETSIGMASK

	// Handle SysProcAttr
	if c.SysProcAttr != nil {
		if c.SysProcAttr.Setpgid {
			flags |= _POSIX_SPAWN_SETPGROUP
			C.set_spawnattr_pgroup(&attr, C.pid_t(c.SysProcAttr.Pgid))
		}
	}

	C.set_spawnattr_flags(&attr, flags)

	// Set signal defaults and masks
	var sigdefault, sigmask C.sigset_t
	C.sigset_fill(&sigdefault)
	C.sigset_empty(&sigmask)
	C.set_spawnattr_sigdefault(&attr, &sigdefault)
	C.set_spawnattr_sigmask(&attr, &sigmask)

	// Convert path to C string
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	// Convert args to C strings
	args := c.Args
	if len(args) == 0 {
		args = []string{c.Path}
	}
	cArgs := make([]*C.char, len(args)+1)
	for i, arg := range args {
		cArgs[i] = C.CString(arg)
		defer C.free(unsafe.Pointer(cArgs[i]))
	}
	cArgs[len(args)] = nil

	// Convert env to C strings
	cEnv := make([]*C.char, len(env)+1)
	for i, e := range env {
		cEnv[i] = C.CString(e)
		defer C.free(unsafe.Pointer(cEnv[i]))
	}
	cEnv[len(env)] = nil

	// Spawn the process
	var pid C.pid_t
	ret := C.do_posix_spawn(&pid, cPath, &fileActions, &attr,
		(**C.char)(unsafe.Pointer(&cArgs[0])),
		(**C.char)(unsafe.Pointer(&cEnv[0])))
	if ret != 0 {
		closeClosers(closersToClose)
		return &Error{Name: c.Path, Err: syscall.Errno(ret)}
	}

	// Close child-side file descriptors in parent
	for _, fd := range closeAfterSpawn {
		unix.Close(fd)
	}

	// Close files that were set up for child
	for _, f := range c.childIOFiles {
		f.Close()
	}
	c.childIOFiles = nil

	c.Process = &Process{Pid: int(pid)}

	// Start goroutines for I/O copying if needed
	c.startGoroutines()

	// Handle context cancellation
	if c.ctx != nil {
		c.watchContext()
	}

	return nil
}

// setupStdin sets up stdin file actions and returns the fd to close after spawn
func (c *Cmd) setupStdin(fileActions *C.posix_spawn_file_actions_t) (int, io.Closer, error) {
	if c.Stdin == nil {
		// Connect to /dev/null
		cDevNull := C.devnull_path()
		if ret := C.add_open_action(fileActions, 0, cDevNull, C.O_RDONLY, 0); ret != 0 {
			return -1, nil, syscall.Errno(ret)
		}
		return -1, nil, nil
	}

	if f, ok := c.Stdin.(*os.File); ok {
		fd := int(f.Fd())
		if ret := C.add_dup2_action(fileActions, C.int(fd), 0); ret != 0 {
			return -1, nil, syscall.Errno(ret)
		}
		return -1, nil, nil
	}

	// Create a pipe for stdin
	pr, pw, err := os.Pipe()
	if err != nil {
		return -1, nil, err
	}
	fd := int(pr.Fd())
	if ret := C.add_dup2_action(fileActions, C.int(fd), 0); ret != 0 {
		pr.Close()
		pw.Close()
		return -1, nil, syscall.Errno(ret)
	}
	c.childIOFiles = append(c.childIOFiles, pr)

	// Start goroutine to copy from c.Stdin to pw
	c.goroutine = append(c.goroutine, func() error {
		_, err := io.Copy(pw, c.Stdin)
		pw.Close()
		return err
	})

	return fd, nil, nil
}

// setupStdout sets up stdout file actions
func (c *Cmd) setupStdout(fileActions *C.posix_spawn_file_actions_t) (int, io.Closer, error) {
	if c.Stdout == nil {
		// Connect to /dev/null
		cDevNull := C.devnull_path()
		if ret := C.add_open_action(fileActions, 1, cDevNull, C.O_WRONLY, 0); ret != 0 {
			return -1, nil, syscall.Errno(ret)
		}
		return -1, nil, nil
	}

	if f, ok := c.Stdout.(*os.File); ok {
		fd := int(f.Fd())
		if ret := C.add_dup2_action(fileActions, C.int(fd), 1); ret != 0 {
			return -1, nil, syscall.Errno(ret)
		}
		return -1, nil, nil
	}

	// Create a pipe for stdout
	pr, pw, err := os.Pipe()
	if err != nil {
		return -1, nil, err
	}
	fd := int(pw.Fd())
	if ret := C.add_dup2_action(fileActions, C.int(fd), 1); ret != 0 {
		pr.Close()
		pw.Close()
		return -1, nil, syscall.Errno(ret)
	}
	c.childIOFiles = append(c.childIOFiles, pw)

	// Start goroutine to copy from pr to c.Stdout
	c.goroutine = append(c.goroutine, func() error {
		_, err := io.Copy(c.Stdout, pr)
		pr.Close()
		return err
	})

	return fd, nil, nil
}

// setupStderr sets up stderr file actions
func (c *Cmd) setupStderr(fileActions *C.posix_spawn_file_actions_t) (int, io.Closer, error) {
	if c.Stderr == nil {
		// Connect to /dev/null
		cDevNull := C.devnull_path()
		if ret := C.add_open_action(fileActions, 2, cDevNull, C.O_WRONLY, 0); ret != 0 {
			return -1, nil, syscall.Errno(ret)
		}
		return -1, nil, nil
	}

	// Check if stdout and stderr are the same writer
	if c.Stderr == c.Stdout {
		// Dup stdout to stderr
		if ret := C.add_dup2_action(fileActions, 1, 2); ret != 0 {
			return -1, nil, syscall.Errno(ret)
		}
		return -1, nil, nil
	}

	if f, ok := c.Stderr.(*os.File); ok {
		fd := int(f.Fd())
		if ret := C.add_dup2_action(fileActions, C.int(fd), 2); ret != 0 {
			return -1, nil, syscall.Errno(ret)
		}
		return -1, nil, nil
	}

	// Create a pipe for stderr
	pr, pw, err := os.Pipe()
	if err != nil {
		return -1, nil, err
	}
	fd := int(pw.Fd())
	if ret := C.add_dup2_action(fileActions, C.int(fd), 2); ret != 0 {
		pr.Close()
		pw.Close()
		return -1, nil, syscall.Errno(ret)
	}
	c.childIOFiles = append(c.childIOFiles, pw)

	// Start goroutine to copy from pr to c.Stderr
	c.goroutine = append(c.goroutine, func() error {
		_, err := io.Copy(c.Stderr, pr)
		pr.Close()
		return err
	})

	return fd, nil, nil
}

// startGoroutines starts the I/O copying goroutines
func (c *Cmd) startGoroutines() {
	c.goroutineErr = make([]error, len(c.goroutine))
	for i, fn := range c.goroutine {
		i, fn := i, fn
		go func() {
			err := fn()
			c.goroutineMu.Lock()
			c.goroutineErr[i] = err
			c.goroutineMu.Unlock()
		}()
	}
}

// watchContext monitors the context and kills the process if it's canceled
func (c *Cmd) watchContext() {
	go func() {
		select {
		case <-c.ctx.Done():
			if c.Process != nil {
				if c.Cancel != nil {
					c.Cancel()
				} else {
					c.Process.Kill()
				}
			}
		}
	}()
}

// Wait waits for the command to exit and waits for any copying to
// stdin or copying from stdout or stderr to complete.
//
// The command must have been started by Start.
//
// The returned error is nil if the command runs, has no problems
// copying stdin, stdout, and stderr, and exits with a zero exit status.
//
// If the command fails to run or doesn't complete successfully, the
// error is of type *ExitError. Other error types may be
// returned for I/O problems.
//
// If any of c.Stdin, c.Stdout or c.Stderr are not an *os.File, Wait also waits
// for the respective I/O loop copying to or from the process to complete.
//
// Wait releases any resources associated with the Cmd.
func (c *Cmd) Wait() error {
	if c.Process == nil {
		return errors.New("exec: not started")
	}
	if c.finished {
		return errors.New("exec: Wait was already called")
	}
	c.finished = true

	// Wait for the process
	state, err := c.Process.Wait()
	if err != nil {
		return err
	}
	c.ProcessState = state

	// Close parent side of pipes to signal EOF to goroutines
	for _, f := range c.parentIOPipes {
		f.Close()
	}
	c.parentIOPipes = nil

	// Wait for I/O goroutines (give them a moment to complete)
	// In a more robust implementation, we'd use a WaitGroup
	var copyErr error
	c.goroutineMu.Lock()
	for _, e := range c.goroutineErr {
		if e != nil && copyErr == nil {
			copyErr = e
		}
	}
	c.goroutineMu.Unlock()

	if !state.Success() {
		return &ExitError{ProcessState: state}
	}

	if copyErr != nil {
		return copyErr
	}

	return nil
}

// closeClosers closes all the closers in the slice
func closeClosers(closers []io.Closer) {
	for _, c := range closers {
		if c != nil {
			c.Close()
		}
	}
}

// isAbs reports whether path is absolute
func isAbs(path string) bool {
	return len(path) > 0 && path[0] == '/'
}

// joinPath joins dir and file
func joinPath(dir, file string) string {
	if isAbs(file) {
		return file
	}
	return dir + "/" + file
}

// closeAfterStart is a helper for closing files after successful Start
type closeAfterStart struct {
	mu    sync.Mutex
	files []*os.File
}

func (c *closeAfterStart) add(f *os.File) {
	c.mu.Lock()
	c.files = append(c.files, f)
	c.mu.Unlock()
}

func (c *closeAfterStart) close() {
	c.mu.Lock()
	for _, f := range c.files {
		f.Close()
	}
	c.files = nil
	c.mu.Unlock()
}
