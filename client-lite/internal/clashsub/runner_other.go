//go:build !windows

package clashsub

import "os/exec"

func hideWindow(*exec.Cmd) {}
