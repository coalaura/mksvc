package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func replaceFile(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}

	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}

	// Same-directory replacement avoids cross-volume copy/delete behavior.
	// WRITE_THROUGH requests completion before reporting success.
	err = windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	if err != nil {
		return &os.LinkError{Op: "replace", Old: source, New: destination, Err: err}
	}

	return nil
}

func syncDirectory(_ string) error {
	// Windows has no portable directory-fsync equivalent; replacements request
	// write-through individually. Filesystem/OS crash guarantees still apply.
	return nil
}
