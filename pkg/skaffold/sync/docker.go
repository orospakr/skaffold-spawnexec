/*
Copyright 2021 The Skaffold Authors

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

package sync

import (
	"context"
	"fmt"
	"io"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/output/log"
	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/util"
	spawnexec "github.com/orospakr/spawnexec"
)

type ContainerSyncer struct{}

func NewContainerSyncer() *ContainerSyncer {
	return &ContainerSyncer{}
}

func (s *ContainerSyncer) Sync(ctx context.Context, _ io.Writer, item *Item) error {
	if len(item.Copy) > 0 {
		log.Entry(ctx).Info("Copying files:", item.Copy, "to", item.Image)
		if _, err := util.RunCmdOut(ctx, s.copyFileFn(ctx, item.Artifact.ImageName, item.Copy)); err != nil {
			return fmt.Errorf("copying files: %w", err)
		}
	}

	if len(item.Delete) > 0 {
		log.Entry(ctx).Info("Deleting files:", item.Delete, "from", item.Image)
		if _, err := util.RunCmdOut(ctx, s.deleteFileFn(ctx, item.Artifact.ImageName, item.Delete)); err != nil {
			return fmt.Errorf("deleting files: %w", err)
		}
	}

	return nil
}

func (s *ContainerSyncer) deleteFileFn(ctx context.Context, containerName string, files syncMap) *spawnexec.Cmd {
	var args []string
	args = append(args, "exec", "-i", containerName, "rm", "-rf", "--")
	for _, dsts := range files {
		args = append(args, dsts...)
	}
	return spawnexec.CommandContext(ctx, "docker", args...)
}

func (s *ContainerSyncer) copyFileFn(ctx context.Context, containerName string, files syncMap) *spawnexec.Cmd {
	reader, writer := io.Pipe()
	go func() {
		if err := util.CreateMappedTar(ctx, writer, "/", files); err != nil {
			writer.CloseWithError(err)
		} else {
			writer.Close()
		}
	}()

	copyCmd := spawnexec.CommandContext(ctx, "docker", "exec", "-i", containerName, "tar", "xmf", "-", "-C", "/", "--no-same-owner")
	copyCmd.Stdin = reader
	return copyCmd
}
