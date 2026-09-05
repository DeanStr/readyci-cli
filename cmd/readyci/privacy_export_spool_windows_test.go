//go:build windows

package main

import (
	"errors"
	"os"
	"testing"
)

func TestCreateSecurePrivacyExportSpoolDeletesOnClose(t *testing.T) {
	spool, err := createSecurePrivacyExportSpool()
	if err != nil {
		t.Fatalf("create secure privacy export spool: %v", err)
	}
	path := spool.Name()
	if err := spool.Close(); err != nil {
		t.Fatalf("close secure privacy export spool: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("privacy export spool remains after close: %v", err)
	}
}
