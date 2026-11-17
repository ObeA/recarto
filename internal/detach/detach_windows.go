//go:build windows

package detach

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// sysProcAttr gives the process a console of its own with no window. The git
// processes it starts share that hidden console. DETACHED_PROCESS would leave
// it with no console, so each of those git processes would open a window.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
