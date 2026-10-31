//go:build !windows

package agentruntime

import "os/exec"

func configureProcess(command *exec.Cmd) {}
