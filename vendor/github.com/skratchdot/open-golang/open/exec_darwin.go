// +build darwin

package open

import (
	exec "github.com/orospakr/spawnexec"
)

func open(input string) *exec.Cmd {
	return exec.Command("open", input)
}

func openWith(input string, appName string) *exec.Cmd {
	return exec.Command("open", "-a", appName, input)
}
