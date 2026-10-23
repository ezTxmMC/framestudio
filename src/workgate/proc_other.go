//go:build !unix

package workgate

import (
	"context"
	"os/exec"
)

// Command on platforms without process groups and signals: a plain child process.
func (g *Impl) Command(ctx context.Context, p Prio, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// Track implements Tracker (nothing to freeze here).
func (g *Impl) Track(cmd *exec.Cmd) func() { return func() {} }

func (g *Impl) syncProcs() {}
