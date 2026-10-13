//go:build !unix

package transport

import "os/exec"

func configureDaemonProcess(_ *exec.Cmd) {}
