//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func replaceFile(source, destination string) error {
	err := os.Rename(source, destination)
	if err != nil {
		return err
	}

	err = syncDirectory(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("file replaced but directory sync failed: %w", err)
	}

	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}

	defer directory.Close()

	return directory.Sync()
}
