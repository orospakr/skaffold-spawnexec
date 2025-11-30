/*
Copyright 2019 The Skaffold Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/output/log"
	spawnexec "github.com/orospakr/spawnexec"
)

// CreateCommand creates an `spawnexec.Cmd` that is configured to call the
// executable (possibly using a wrapper in `workingDir`, when found) with the given arguments,
// with working directory set to `workingDir`.
func (cw CommandWrapper) CreateCommand(ctx context.Context, workingDir string, args []string) spawnexec.Cmd {
	executable := cw.Executable

	if cw.Wrapper != "" && !SkipWrapperCheck {
		for _, extension := range []string{".cmd", ".bat"} {
			wrapper := cw.Wrapper + extension
			if wrapperExecutable, err := AbsFile(workingDir, wrapper); err == nil {
				log.Entry(ctx).Debugf("Using wrapper %s for %s", wrapper, cw.Executable)
				executable = "cmd"
				args = append([]string{"/c", wrapperExecutable}, args...)
				break
			}
		}
	}

	cmd := spawnexec.CommandContext(ctx, executable, args...)
	cmd.Dir = workingDir
	return *cmd
}
