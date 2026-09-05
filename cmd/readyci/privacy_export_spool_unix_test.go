//go:build !windows

package main

import (
	"errors"
	"os"
	"testing"
)

func TestCreateSecurePrivacyExportSpoolIsUnlinked(t *testing.T) {
	spool, err := createSecurePrivacyExportSpool()
	if err != nil {
		t.Fatalf("create secure privacy export spool: %v", err)
	}
	defer spool.Close()
	if _, err := os.Stat(spool.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("privacy export spool remains addressable by path: %v", err)
	}
}
