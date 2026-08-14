//go:build windows

package gitops

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func configureGitProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
func stopGitProcess(p *os.Process) { _ = p.Signal(os.Interrupt) }
func killGitProcess(p *os.Process) {
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(p.Pid), "/T", "/F").Run()
	_ = p.Kill()
}
