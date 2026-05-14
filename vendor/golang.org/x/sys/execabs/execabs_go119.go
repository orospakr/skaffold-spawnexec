// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.19

package execabs

import (
	"errors"

	exec "github.com/orospakr/spawnexec"
)

func isGo119ErrDot(err error) bool {
	return errors.Is(err, exec.ErrDot)
}

// spawnexec.Cmd has no Err field; fixCmd always applies its own check.
func isGo119ErrFieldSet(cmd *exec.Cmd) bool {
	return false
}
