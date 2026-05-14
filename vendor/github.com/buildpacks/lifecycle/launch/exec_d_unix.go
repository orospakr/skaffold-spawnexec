//go:build unix

package launch

import (
	"os"
	exec "github.com/orospakr/spawnexec"
)

func setHandle(cmd *exec.Cmd, f *os.File) error {
	cmd.ExtraFiles = []*os.File{f}
	return nil
}
