//go:build !linux

package commandconn

import (
	exec "github.com/orospakr/spawnexec"
)

func setPdeathsig(*exec.Cmd) {}
