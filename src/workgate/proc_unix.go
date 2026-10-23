//go:build !windows

package workgate

import (
	"os/exec"
	"syscall"
)

func prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// lower sets the nice value of a started process to 19.
func lower(pid int) { _ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, 19) }

// signalGroup stops or continues the process group of pid; false when it no longer exists.
func signalGroup(pid int, stop bool) bool {
	sig := syscall.SIGCONT
	if stop {
		sig = syscall.SIGSTOP
	}
	err := syscall.Kill(-pid, sig)
	return err != syscall.ESRCH
}
