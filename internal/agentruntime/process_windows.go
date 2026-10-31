//go:build windows

package agentruntime

import (
	"os/exec"
	"syscall"
)

// CREATE_NEW_PROCESS_GROUP keeps the agent out of the server console group so a
// Ctrl+C aimed at the launcher does not take the agent down with it.
func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200}
}
