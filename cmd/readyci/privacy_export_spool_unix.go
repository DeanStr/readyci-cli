//go:build !windows

package main

import "os"

func createSecurePrivacyExportSpool() (*os.File, error) {
	temp, err := os.CreateTemp("", "readyci-privacy-export-*.json")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(temp.Name()); err != nil {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return nil, err
	}
	return temp, nil
}
