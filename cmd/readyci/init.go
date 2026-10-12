package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const remoteCIToolchainImage = "ubuntu-24.04-ci-docker"

func initConfigPath(force bool) (string, error) {
	for _, existingPath := range []string{".readyci.yml", ".readyci.yaml"} {
		if _, err := os.Stat(existingPath); err == nil {
			if force {
				return existingPath, nil
			}
			return "", fmt.Errorf("%s already exists; use --force to overwrite", existingPath)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return ".readyci.yml", nil
}

func writeSetupConfig(path, content string, force bool) error {
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if !info.Mode().IsRegular() {
			return cliConfigError("configuration write target must be a regular file: "+path, nil)
		}
		if !force {
			return cliConfigError("configuration already exists; run setup again to review it", nil)
		}
	} else {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteString(content)
		return errors.Join(writeErr, file.Close())
	}

	// Replacing the directory entry avoids following a symlink swapped in after
	// the check above and preserves the previous configuration on write failure.
	file, err := os.CreateTemp(filepath.Dir(path), ".readyci-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(info.Mode().Perm()); err != nil {
		file.Close()
		return err
	}
	_, writeErr := file.WriteString(content)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func inferProjectID(cwd string) string {
	base := strings.ToLower(filepath.Base(cwd))
	var out strings.Builder
	lastDash := false
	for _, r := range base {
		allowed := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.'
		if allowed {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	value := strings.Trim(out.String(), "-.")
	if value == "" {
		return "proj_dev"
	}
	return value
}
