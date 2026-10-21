package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func watchAffectedRules(cwd string, cfg config.ProjectConfig, configPath string, resolveOptions checkResolveOptions, interval time.Duration, token string) error {
	if interval <= 0 {
		return cliConfigError("--watch-interval must be positive", nil)
	}
	ctx, stop := signalContext()
	defer stop()

	fingerprint, err := affectedWorkspaceFingerprint(ctx, cwd, cfg, resolveOptions)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "readyci: watching affected rules every %s\n", interval)
	if err := runAffectedRulesOnce(ctx, cwd, cfg, configPath, resolveOptions, token); err != nil {
		if !isSuccessfulExit(err) {
			var exitErr exitCodeError
			if !errors.As(err, &exitErr) {
				return err
			}
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			next, err := affectedWorkspaceFingerprint(ctx, cwd, cfg, resolveOptions)
			if err != nil {
				fmt.Fprintf(os.Stderr, "readyci: watch skipped: %v\n", err)
				continue
			}
			if next == fingerprint {
				continue
			}
			fingerprint = next
			fmt.Fprintln(os.Stderr, "readyci: change detected")
			if err := runAffectedRulesOnce(ctx, cwd, cfg, configPath, resolveOptions, token); err != nil {
				if !isSuccessfulExit(err) {
					var exitErr exitCodeError
					if errors.As(err, &exitErr) {
						continue
					}
					return err
				}
			}
		}
	}
}

func affectedWorkspaceFingerprint(ctx context.Context, cwd string, cfg config.ProjectConfig, resolveOptions checkResolveOptions) (string, error) {
	changeMode := strings.TrimSpace(resolveOptions.Changes)
	if changeMode == "" {
		changeMode = syncbundle.ChangeModeWorking
	}
	syncConfigs := make(map[string]config.SyncConfig)
	var keys []string
	for _, checkName := range affectedRoutingCheckNames(cfg) {
		check := cfg.Checks[checkName]
		syncConfig := effectiveAffectedCheckSyncConfig(cfg.Sync, check.Sync, changeMode)
		key := affectedSyncConfigKey(syncConfig)
		if _, ok := syncConfigs[key]; ok {
			continue
		}
		syncConfigs[key] = syncConfig
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return workspaceFingerprint(ctx, workspaceFingerprintOptions{ChangeMode: changeMode})
	}
	sort.Strings(keys)
	digest := sha256.New()
	for _, key := range keys {
		syncConfig := syncConfigs[key]
		fingerprint, err := workspaceFingerprint(ctx, workspaceFingerprintOptions{
			ExcludePatterns: append([]string(nil), syncConfig.Exclude...), ReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), syncConfig.ReviewedFixtures...),
			IncludeUntracked: syncConfig.IncludeUntracked,
			ChangeMode:       changeMode,
		})
		if err != nil {
			return "", err
		}
		hashSection(digest, "affected-sync:"+key, []byte(fingerprint))
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
