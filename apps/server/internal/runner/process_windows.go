//go:build windows

package runner

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
func gentleStop(p *os.Process) error { return p.Signal(os.Interrupt) }
func forceStop(p *os.Process) error {
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(p.Pid), "/T", "/F").Run()
	return p.Kill()
}
