//go:build unix

package workgate

import (
	"context"
	"os/exec"
	"syscall"
)

// Command implements Gate. Background commands are prefixed with nice -n 19 and
// ionice -c3 when those tools exist (both exec the real program, so pid and process
// group stay the same) and get their own process group so they can be frozen whole.
func (g *Impl) Command(ctx context.Context, p Prio, name string, args ...string) *exec.Cmd {
	if p >= PrioVisible {
		if ionice, err := exec.LookPath("ionice"); err == nil {
			args = append([]string{"-c3", name}, args...)
			name = ionice
		}
		if nice, err := exec.LookPath("nice"); err == nil {
			args = append([]string{"-n", "19", name}, args...)
			name = nice
		}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if p >= PrioVisible {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		g.mu.Lock()
		g.procs[cmd] = &proc{prio: p}
		g.mu.Unlock()
	}
	return cmd
}

// Track implements Tracker: a started background command is frozen at once when the
// state asks for it and from then on follows the state. Other commands are ignored.
func (g *Impl) Track(cmd *exec.Cmd) func() {
	g.mu.Lock()
	_, ok := g.procs[cmd]
	g.mu.Unlock()
	if !ok || cmd.Process == nil {
		return func() {}
	}
	g.syncProcs()
	return func() {
		g.mu.Lock()
		delete(g.procs, cmd)
		g.mu.Unlock()
	}
}

// syncProcs freezes or resumes tracked processes to match the state.
func (g *Impl) syncProcs() {
	st := g.State()
	g.mu.Lock()
	defer g.mu.Unlock()
	for cmd, pr := range g.procs {
		if cmd.Process == nil {
			continue
		}
		want := st == Playing || (st == Scrubbing && pr.prio >= PrioBackground)
		if want == pr.stopped {
			continue
		}
		sig := syscall.SIGCONT
		if want {
			sig = syscall.SIGSTOP
		}
		if syscall.Kill(-cmd.Process.Pid, sig) != nil {
			_ = cmd.Process.Signal(sig)
		}
		pr.stopped = want
	}
}
