//go:build !windows

package gitops

import (
	"os"
	"os/exec"
	"syscall"
)

func configureGitProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func stopGitProcess(p *os.Process)      { _ = syscall.Kill(-p.Pid, syscall.SIGTERM) }
func killGitProcess(p *os.Process)      { _ = syscall.Kill(-p.Pid, syscall.SIGKILL) }
