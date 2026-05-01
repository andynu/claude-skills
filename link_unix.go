//go:build !windows

package main

import "os"

func linkDir(source, target string) error {
	return os.Symlink(source, target)
}
