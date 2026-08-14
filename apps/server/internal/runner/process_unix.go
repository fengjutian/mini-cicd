//go:build !windows

package runner

import (
	"os"
	"os/exec"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func gentleStop(p *os.Process) error { return syscall.Kill(-p.Pid, syscall.SIGTERM) }
func forceStop(p *os.Process) error  { return syscall.Kill(-p.Pid, syscall.SIGKILL) }
