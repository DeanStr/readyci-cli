package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

type setupConfigFile struct {
	info os.FileInfo
	data []byte
}

type setupConfigState struct {
	selected string
	files    map[string]setupConfigFile
}

func readSetupConfigState() (setupConfigState, error) {
	_, selected, err := config.LoadProjectConfig(".")
	if err != nil && selected == "" {
		return setupConfigState{}, err
	}
	state := setupConfigState{selected: selected, files: make(map[string]setupConfigFile)}
	paths := []string{".readyci.yml", ".readyci.yaml"}
	if selected != "" {
		paths = append(paths, selected)
	}
	for _, path := range paths {
		path, err = filepath.Abs(path)
		if err != nil {
			return setupConfigState{}, err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			state.files[path] = setupConfigFile{}
			continue
		}
		if err != nil {
			return setupConfigState{}, err
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return setupConfigState{}, cliConfigError("configuration must be a regular file: "+path, nil)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return setupConfigState{}, err
		}
		state.files[path] = setupConfigFile{info: info, data: data}
	}
	return state, nil
}

func (state setupConfigState) verify() error {
	current, err := readSetupConfigState()
	changed := cliConfigError("configuration changed during setup; run readyci setup again to review it", err)
	if err != nil || state.selected != current.selected || len(state.files) != len(current.files) {
		return changed
	}
	for path, original := range state.files {
		latest, exists := current.files[path]
		if !exists || !bytes.Equal(original.data, latest.data) {
			return changed
		}
		if original.info == nil && latest.info == nil {
			continue
		}
		if original.info == nil || latest.info == nil || !os.SameFile(original.info, latest.info) || original.info.Mode() != latest.info.Mode() {
			return changed
		}
	}
	return nil
}
