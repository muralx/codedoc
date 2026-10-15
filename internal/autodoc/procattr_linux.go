package autodoc

import "syscall"

// procAttr puts claude in its own process group, killed as a whole on
// cancellation, and has the kernel kill it should codedoc die first.
func procAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
