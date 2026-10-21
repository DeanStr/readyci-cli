package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
	"gopkg.in/yaml.v3"
)

// Error identifies failures while locating, reading, or parsing Ready CI
// project configuration. CLI callers use this marker to distinguish invalid
// configuration from an internal implementation failure.
type Error struct{ Err error }

func (e Error) Error() string { return e.Err.Error() }
func (e Error) Unwrap() error { return e.Err }

func configError(err error) error {
	if err == nil {
		return nil
	}
	var typed Error
	if errors.As(err, &typed) {
		return err
	}
	return Error{Err: err}
}

func DefaultProjectConfig() ProjectConfig {
	return ProjectConfig{
		Version:    1,
		Image:      preflightapi.DefaultImage,
		RunnerSize: "standard",
		CacheMode:  "cache-only",
	}
}

func LoadProjectConfig(cwd string) (ProjectConfig, string, error) {
	cfg := DefaultProjectConfig()
	path, err := findProjectConfig(cwd)
	if err != nil {
		return cfg, "", configError(err)
	}
	if path == "" {
		return cfg, "", nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, "", configError(err)
	}
	cfg, err = ParseProjectConfig(data, path)
	return cfg, path, configError(err)
}

func ParseProjectConfig(data []byte, source string) (ProjectConfig, error) {
	cfg := DefaultProjectConfig()
	var loaded ProjectConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&loaded); err != nil {
		if errors.Is(err, io.EOF) {
			return cfg, nil
		}
		return cfg, configError(fmt.Errorf("parse %s: %w", source, err))
	}
	if loaded.Version != 0 && loaded.Version != 1 {
		return cfg, configError(fmt.Errorf("%s: unsupported config version %d", source, loaded.Version))
	}
	cfg.merge(loaded)
	if err := syncbundle.ValidateExcludePatterns(cfg.Sync.Exclude); err != nil {
		return cfg, configError(fmt.Errorf("%s: sync.exclude: %w", source, err))
	}
	var err error
	cfg.Sync.ReviewedFixtures, err = syncbundle.NormalizeReviewedFixtures(cfg.Sync.ReviewedFixtures)
	if err != nil {
		return cfg, configError(fmt.Errorf("%s: sync.reviewed_fixtures: %w", source, err))
	}
	if cfg.Sync.RespectGitignore != nil && !*cfg.Sync.RespectGitignore {
		return cfg, configError(fmt.Errorf("%s: sync.respect_gitignore=false is not supported yet", source))
	}
	for name, check := range cfg.Checks {
		check.RuntimeRequirements, err = preflightapi.NormalizeRuntimeRequirements(check.RuntimeRequirements)
		if err != nil {
			return cfg, configError(fmt.Errorf("%s: checks.%s.runtime_requirements: %w", source, name, err))
		}
		if err := syncbundle.ValidateExcludePatterns(check.Sync.Exclude); err != nil {
			return cfg, configError(fmt.Errorf("%s: checks.%s.sync.exclude: %w", source, name, err))
		}
		check.Sync.ReviewedFixtures, err = syncbundle.NormalizeReviewedFixtures(check.Sync.ReviewedFixtures)
		if err != nil {
			return cfg, configError(fmt.Errorf("%s: checks.%s.sync.reviewed_fixtures: %w", source, name, err))
		}
		cfg.Checks[name] = check
		if check.Sync.RespectGitignore != nil && !*check.Sync.RespectGitignore {
			return cfg, configError(fmt.Errorf("%s: checks.%s.sync.respect_gitignore=false is not supported yet", source, name))
		}
	}
	if err := ValidateDependencyChecks(cfg); err != nil {
		return cfg, configError(fmt.Errorf("%s: %w", source, err))
	}
	return cfg, nil
}

func (cfg *ProjectConfig) merge(loaded ProjectConfig) {
	if loaded.Version != 0 {
		cfg.Version = loaded.Version
	}
	if loaded.ProjectID != "" {
		cfg.ProjectID = loaded.ProjectID
	}
	if loaded.Region != "" {
		cfg.Region = loaded.Region
	}
	if loaded.Image != "" {
		cfg.Image = loaded.Image
	}
	if loaded.RunnerSize != "" {
		cfg.RunnerSize = loaded.RunnerSize
	}
	if loaded.TimeoutSeconds != 0 {
		cfg.TimeoutSeconds = loaded.TimeoutSeconds
	}
	if loaded.Timeout != "" {
		cfg.Timeout = loaded.Timeout
	}
	if loaded.Retries != 0 {
		cfg.Retries = loaded.Retries
	}
	if loaded.ConcurrencyGroup != "" {
		cfg.ConcurrencyGroup = loaded.ConcurrencyGroup
	}
	if loaded.ConcurrencyLimit != 0 {
		cfg.ConcurrencyLimit = loaded.ConcurrencyLimit
	}
	if loaded.CacheMode != "" {
		cfg.CacheMode = loaded.CacheMode
	}
	if loaded.Cache.Mode != "" {
		cfg.CacheMode = loaded.Cache.Mode
	}
	if loaded.Cache.Size != "" {
		cfg.Cache.Size = loaded.Cache.Size
	}
	if len(loaded.Cache.Paths) > 0 {
		cfg.Cache.Paths = append([]string(nil), loaded.Cache.Paths...)
	}
	if len(loaded.Cache.KeyFiles) > 0 {
		cfg.Cache.KeyFiles = append([]string(nil), loaded.Cache.KeyFiles...)
	}
	if len(loaded.Artifacts.Paths) > 0 {
		cfg.Artifacts.Paths = append([]string(nil), loaded.Artifacts.Paths...)
	}
	cfg.Network = loaded.Network
	cfg.Sync = loaded.Sync
	cfg.Resources = loaded.Resources
	cfg.Affected = loaded.Affected
	cfg.Services = loaded.Services
	if len(loaded.Checks) > 0 {
		cfg.Checks = loaded.Checks
	}
}

func findProjectConfig(cwd string) (string, error) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	for {
		for _, name := range []string{".readyci.yml", ".readyci.yaml"} {
			path := filepath.Join(dir, name)
			if info, err := os.Stat(path); err == nil {
				if info.IsDir() {
					return "", fmt.Errorf("%s is a directory", path)
				}
				return path, nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}
