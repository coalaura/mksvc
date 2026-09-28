package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	err := checkRegularTarget(path)
	if err != nil {
		return err
	}

	// Use host path semantics: a Windows backslash must not put the temporary
	// file in the working directory or on a different filesystem.
	file, err := os.CreateTemp(filepath.Dir(path), ".mksvc-*")
	if err != nil {
		return err
	}

	tempPath := file.Name()

	defer os.Remove(tempPath)
	defer file.Close()

	err = file.Chmod(mode)
	if err != nil {
		return err
	}

	_, err = file.Write(data)
	if err != nil {
		return err
	}

	err = file.Sync()
	if err != nil {
		return err
	}

	err = file.Close()
	if err != nil {
		return err
	}

	// Never unlink the destination as a fallback. A failed native replacement
	// must leave the previous file available, including on Windows.
	return replaceFile(tempPath, path)
}

func checkRegularTarget(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to replace non-regular file %s", path)
	}

	return nil
}
