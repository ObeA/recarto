//go:build !windows

package detach

import "syscall"

// sysProcAttr puts the process in a session of its own: a host that hangs up
// or kills the hook's process group when its session ends does not reach it.
func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
