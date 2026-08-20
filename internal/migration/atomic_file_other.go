//go:build !windows

package migration

import "os"

func replaceFile(source, target string) error { return os.Rename(source, target) }

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
