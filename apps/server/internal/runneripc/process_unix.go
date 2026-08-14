//go:build !windows

package runneripc

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureProcess(cmd *exec.Cmd, uid, gid int) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{uint32(gid)}}}
}
func stopProcess(p *os.Process) {
	_ = syscall.Kill(-p.Pid, syscall.SIGTERM)
	time.Sleep(2 * time.Second)
	_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
}
func cleanupProcess(p *os.Process) { _ = syscall.Kill(-p.Pid, syscall.SIGKILL) }
