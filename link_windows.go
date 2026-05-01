//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// linkDir tries a directory symlink first (works when Developer Mode is on
// or the process is elevated). On failure it falls back to a junction via
// `cmd /c mklink /J`, which works without admin rights.
func linkDir(source, target string) error {
	if err := os.Symlink(source, target); err == nil {
		return nil
	}
	cmd := exec.Command("cmd", "/c", "mklink", "/J", target, source)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J: %w: %s", err, string(out))
	}
	return nil
}
