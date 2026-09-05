//go:build unix

package testexec

import (
	"os/exec"
	"syscall"
)

// reapGroup puts the child in its own process group and makes cancellation
// kill the whole group, so a `bash <hook>` that started `aiagentmemory` (or
// anything else) takes it down too. Killing only the direct child is what
// reparents the grandchildren to launchd/init, which is the incident this
// package exists for.
func reapGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
