//go:build windows

package workgate

import (
	"os/exec"
	"syscall"
)

const belowNormalPriorityClass = 0x00004000

func prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= belowNormalPriorityClass
}

func lower(int) {}

// signalGroup cannot suspend processes on Windows: callers yield between steps instead;
// the process keeps running at below normal priority.
func signalGroup(int, bool) bool { return true }
