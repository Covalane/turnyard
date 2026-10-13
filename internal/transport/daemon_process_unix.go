//go:build unix

package transport

import (
	"os/exec"
	"syscall"
)

func configureDaemonProcess(cmd *exec.Cmd) {
	// The supervisor outlives the CLI and its terminal or job runner. Releasing
	// the Process handle alone does not detach it from the launcher's group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
