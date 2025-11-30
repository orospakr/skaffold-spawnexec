package spawnexec

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Process stores the information about a process created by Start.
type Process struct {
	Pid int
}

// Kill causes the Process to exit immediately. Kill does not wait until
// the Process has actually exited. This only kills the Process itself,
// not any other processes it may have started.
func (p *Process) Kill() error {
	return p.Signal(syscall.SIGKILL)
}

// Signal sends a signal to the Process.
func (p *Process) Signal(sig os.Signal) error {
	if p.Pid <= 0 {
		return os.ErrInvalid
	}
	s, ok := sig.(syscall.Signal)
	if !ok {
		return os.ErrInvalid
	}
	return unix.Kill(p.Pid, s)
}

// Release releases any resources associated with the Process p,
// rendering it unusable in the future.
// Release only needs to be called if Wait is not.
func (p *Process) Release() error {
	// For posix_spawn-based processes, there's not much to release
	// since we don't keep extra file descriptors open.
	p.Pid = -1
	return nil
}

// Wait waits for the Process to exit, and then returns a
// ProcessState describing its status and an error, if any.
// Wait releases any resources associated with the Process.
func (p *Process) Wait() (*ProcessState, error) {
	if p.Pid <= 0 {
		return nil, os.ErrInvalid
	}
	var status unix.WaitStatus
	var rusage unix.Rusage
	pid, err := unix.Wait4(p.Pid, &status, 0, &rusage)
	if err != nil {
		return nil, err
	}
	return &ProcessState{
		pid:    pid,
		status: status,
		rusage: &rusage,
	}, nil
}

// ProcessState stores information about a process, as reported by Wait.
type ProcessState struct {
	pid    int             // The process's id.
	status unix.WaitStatus // The status returned by wait syscall
	rusage *unix.Rusage    // Resource usage info
}

// Pid returns the process id of the exited process.
func (p *ProcessState) Pid() int {
	return p.pid
}

// Exited reports whether the program has exited.
// On Unix systems this reports true if the program exited due to calling exit,
// but false if the program terminated due to a signal.
func (p *ProcessState) Exited() bool {
	return p.status.Exited()
}

// Success reports whether the program exited successfully,
// such as with exit status 0 on Unix.
func (p *ProcessState) Success() bool {
	return p.status.ExitStatus() == 0
}

// ExitCode returns the exit code of the exited process, or -1
// if the process hasn't exited or was terminated by a signal.
func (p *ProcessState) ExitCode() int {
	if !p.status.Exited() {
		return -1
	}
	return p.status.ExitStatus()
}

// Sys returns system-dependent exit information about
// the process.
func (p *ProcessState) Sys() interface{} {
	return p.status
}

// SysUsage returns system-dependent resource usage information about
// the exited process.
func (p *ProcessState) SysUsage() interface{} {
	return p.rusage
}

// SystemTime returns the system CPU time of the exited process and its children.
func (p *ProcessState) SystemTime() time.Duration {
	if p.rusage == nil {
		return 0
	}
	return time.Duration(p.rusage.Stime.Nano()) * time.Nanosecond
}

// UserTime returns the user CPU time of the exited process and its children.
func (p *ProcessState) UserTime() time.Duration {
	if p.rusage == nil {
		return 0
	}
	return time.Duration(p.rusage.Utime.Nano()) * time.Nanosecond
}

// String returns a human-readable string representation of the ProcessState.
func (p *ProcessState) String() string {
	if p == nil {
		return "<nil>"
	}
	status := p.Sys().(unix.WaitStatus)
	switch {
	case status.Exited():
		code := status.ExitStatus()
		if code == 0 {
			return "exit status 0"
		}
		return fmt.Sprintf("exit status %d", code)
	case status.Signaled():
		sig := status.Signal()
		s := sig.String()
		if status.CoreDump() {
			s += " (core dumped)"
		}
		return "signal: " + s
	case status.Stopped():
		sig := status.StopSignal()
		return "stop signal: " + sig.String()
	case status.Continued():
		return "continued"
	}
	return fmt.Sprintf("unknown status: %v", status)
}
