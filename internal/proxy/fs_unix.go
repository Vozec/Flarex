//go:build unix

package proxy

import "os"

func removeIfExists(path string) error {
	if _, err := os.Stat(path); err == nil {
		return os.Remove(path)
	}
	return nil
}

func chmod(path string, mode uint32) error {
	return os.Chmod(path, os.FileMode(mode))
}
