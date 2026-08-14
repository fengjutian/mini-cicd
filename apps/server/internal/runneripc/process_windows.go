//go:build windows

package runneripc

import (
	"os"
	"os/exec"
)

func configureProcess(cmd *exec.Cmd, uid, gid int) {}
func stopProcess(p *os.Process)                    { _ = p.Kill() }
func cleanupProcess(p *os.Process)                 { _ = p.Kill() }
