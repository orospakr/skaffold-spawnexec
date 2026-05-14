package credentials

import (
	exec "github.com/orospakr/spawnexec"
)

func defaultCredentialsStore() string {
	if _, err := exec.LookPath("pass"); err == nil {
		return "pass"
	}

	return "secretservice"
}
