//go:build windows

package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

const (
	windowsDeleteAccess           = 0x00010000
	windowsFileAttributeTemporary = 0x00000100
	windowsFileFlagDeleteOnClose  = 0x04000000
)

func createSecurePrivacyExportSpool() (*os.File, error) {
	for range 100 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		path := filepath.Join(os.TempDir(), "readyci-privacy-export-"+hex.EncodeToString(random[:])+".json")
		pathUTF16, err := syscall.UTF16PtrFromString(path)
		if err != nil {
			return nil, err
		}
		// DELETE_ON_CLOSE is valid only when the handle itself has DELETE access.
		// Delete sharing also ensures the kernel can complete disposition when this
		// process exits while another system component briefly holds the path.
		handle, err := syscall.CreateFile(
			pathUTF16,
			syscall.GENERIC_READ|syscall.GENERIC_WRITE|windowsDeleteAccess,
			syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
			nil,
			syscall.CREATE_NEW,
			windowsFileAttributeTemporary|windowsFileFlagDeleteOnClose,
			0,
		)
		if err == nil {
			file := os.NewFile(uintptr(handle), path)
			if file == nil {
				_ = syscall.CloseHandle(handle)
				return nil, errors.New("could not wrap privacy export spool handle")
			}
			return file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	return nil, errors.New("could not allocate a unique privacy export spool")
}
