//go:build !unix

package gateway

import "os/exec"

// probeProcessGroups is false where the platform has no process groups, so the
// stop test does not assert that a probe's children are gone.
const probeProcessGroups = false

// isolateProbeProcess is a no-op without process groups: cancelling the command
// context kills the interpreter itself, and `cmd /C` owns no POSIX pipeline.
func isolateProbeProcess(*exec.Cmd) {}
