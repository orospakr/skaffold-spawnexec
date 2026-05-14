//go:build !windows

package commandconn

import (
	exec "github.com/orospakr/spawnexec"
)

func createSession(cmd *exec.Cmd) {
	// Setsid not available on spawnexec.SysProcAttr; SSH ProxyCommand session
	// creation is skipped. spawnexec uses posix_spawn which avoids the macOS
	// atfork hang bug that necessitates this fork.
	_ = cmd
}
