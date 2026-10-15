//go:build !linux

package autodoc

import "syscall"

// procAttr puts claude in its own process group, killed as a whole on
// cancellation.
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
