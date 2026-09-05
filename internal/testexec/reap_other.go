//go:build !unix

package testexec

import "os/exec"

// reapGroup on a platform without process groups leaves exec's default
// cancel in place, which kills the direct child only. The deadline still
// holds; only the grandchild reaping is weaker, and no CI runner here is
// such a platform.
func reapGroup(*exec.Cmd) {}
