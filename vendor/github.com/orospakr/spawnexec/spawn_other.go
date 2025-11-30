//go:build !darwin

package spawnexec

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// On non-darwin platforms, we fall back to using os/exec.
// This provides API compatibility while not benefiting from posix_spawn.

// Start starts the specified command but does not wait for it to complete.
// On non-darwin platforms, this falls back to os/exec.
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

	// Create the underlying os/exec.Cmd
	var osCmd *exec.Cmd
	if c.ctx != nil {
		osCmd = exec.CommandContext(c.ctx, c.Path, c.Args[1:]...)
	} else {
		osCmd = exec.Command(c.Path, c.Args[1:]...)
	}

	osCmd.Dir = c.Dir
	osCmd.Env = c.Env
	osCmd.Stdin = c.Stdin
	osCmd.Stdout = c.Stdout
	osCmd.Stderr = c.Stderr
	osCmd.ExtraFiles = c.ExtraFiles

	if c.SysProcAttr != nil {
		osCmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid:    c.SysProcAttr.Setpgid,
			Setctty:    c.SysProcAttr.Setctty,
			Noctty:     c.SysProcAttr.Noctty,
			Ctty:       c.SysProcAttr.Ctty,
			Foreground: c.SysProcAttr.Foreground,
			Pgid:       c.SysProcAttr.Pgid,
		}
	}

	if err := osCmd.Start(); err != nil {
		return err
	}

	// Store the process
	c.Process = &Process{Pid: osCmd.Process.Pid}

	// Store reference to os/exec.Cmd for Wait
	c.osCmd = osCmd

	return nil
}

// Wait waits for the command to exit.
// On non-darwin platforms, this falls back to os/exec.
func (c *Cmd) Wait() error {
	if c.Process == nil {
		return errors.New("exec: not started")
	}
	if c.finished {
		return errors.New("exec: Wait was already called")
	}
	c.finished = true

	osCmd, ok := c.osCmd.(*exec.Cmd)
	if !ok || osCmd == nil {
		return errors.New("exec: internal error: osCmd is nil or wrong type")
	}

	err := osCmd.Wait()

	// Convert os.ProcessState to our ProcessState
	if osCmd.ProcessState != nil {
		ps := osCmd.ProcessState
		var rusage *unix.Rusage
		if r, ok := ps.SysUsage().(*syscall.Rusage); ok && r != nil {
			rusage = convertSyscallRusage(r)
		}
		c.ProcessState = &ProcessState{
			pid:    ps.Pid(),
			status: unix.WaitStatus(ps.Sys().(syscall.WaitStatus)),
			rusage: rusage,
		}
	}

	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return &ExitError{ProcessState: c.ProcessState}
		}
		return err
	}

	return nil
}

// convertSyscallRusage converts syscall.Rusage to unix.Rusage
func convertSyscallRusage(r *syscall.Rusage) *unix.Rusage {
	if r == nil {
		return nil
	}
	return &unix.Rusage{
		Utime:    unix.Timeval{Sec: r.Utime.Sec, Usec: int32(r.Utime.Usec)},
		Stime:    unix.Timeval{Sec: r.Stime.Sec, Usec: int32(r.Stime.Usec)},
		Maxrss:   r.Maxrss,
		Ixrss:    r.Ixrss,
		Idrss:    r.Idrss,
		Isrss:    r.Isrss,
		Minflt:   r.Minflt,
		Majflt:   r.Majflt,
		Nswap:    r.Nswap,
		Inblock:  r.Inblock,
		Oublock:  r.Oublock,
		Msgsnd:   r.Msgsnd,
		Msgrcv:   r.Msgrcv,
		Nsignals: r.Nsignals,
		Nvcsw:    r.Nvcsw,
		Nivcsw:   r.Nivcsw,
	}
}

// hasChdir reports whether posix_spawn_file_actions_addchdir_np is available.
// On non-darwin, this is not applicable.
func hasChdir() bool {
	return true // os/exec handles Dir properly
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

// Placeholder types for non-darwin build
type closeAfterStart struct{}

func (c *closeAfterStart) add(f *os.File) {}
func (c *closeAfterStart) close()         {}
