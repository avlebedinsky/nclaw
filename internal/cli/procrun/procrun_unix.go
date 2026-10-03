//go:build unix

package procrun

import (
	"errors"
	"log"
	"os/exec"
	"syscall"
)

func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return signalGroup(cmd, syscall.SIGTERM)
	}
}

func sweep(cmd *exec.Cmd) {
	if err := signalGroup(cmd, syscall.SIGKILL); err != nil {
		log.Printf("procrun: kill process group: %v", err)
	}
}

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
