//go:build unix

package gateway

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// probeProcessGroups reports whether isolateProbeProcess kills the whole
// process group of a probe. Tests use it to skip assertions that only hold where
// the platform has process groups.
const probeProcessGroups = true

// isolateProbeProcess puts a probe in its own process group and makes the probe
// timeout and cancellation kill the whole group. Killing only the shell would
// leave a pipeline such as `curl … | grep …` running after the timeout, and a
// stopped probe set would keep spawning those strays.
func isolateProbeProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		// The child leads its own group, so its pid is also the group id and a
		// negative pid signals every member of the group.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return cmd.Process.Kill()
		}
		return nil
	}
}
