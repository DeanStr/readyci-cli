package verificationplan

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/netpolicy"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// ProjectCacheMode resolves the project default cache mode.
func ProjectCacheMode(cfg config.ProjectConfig) string {
	return defaultString(cfg.CacheMode, cfg.Cache.Mode)
}

// NormalizeCacheMode validates and canonicalizes a cache mode.
func NormalizeCacheMode(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch value {
	case "", "cache-only", "ephemeral":
		return value, nil
	default:
		return "", fmt.Errorf("invalid cache mode %q", value)
	}
}

// CacheLaneSizeForMode zeroes the lane size for modes that do not use one.
func CacheLaneSizeForMode(cacheMode string, sizeBytes int64) int64 {
	if strings.TrimSpace(cacheMode) == "ephemeral" {
		return 0
	}
	return sizeBytes
}

// ValidateRunnerSize validates a runner size name.
func ValidateRunnerSize(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if _, ok := preflightapi.RunnerSizeSpecFor(value); !ok {
		return fmt.Errorf("unsupported runner_size %q", value)
	}
	return nil
}

// ParseByteSize parses a human byte size such as "2GiB".
func ParseByteSize(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	split := 0
	for split < len(value) {
		r, size := utf8.DecodeRuneInString(value[split:])
		if !unicode.IsDigit(r) && r != '.' {
			break
		}
		split += size
	}
	if split == 0 {
		return 0, fmt.Errorf("invalid byte size %q", value)
	}
	numberText := strings.TrimSpace(value[:split])
	unit := strings.ToLower(strings.TrimSpace(value[split:]))
	if strings.Contains(numberText, ".") {
		return 0, fmt.Errorf("byte size %q must be a whole number", value)
	}
	number, err := strconv.ParseInt(numberText, 10, 64)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("invalid byte size %q", value)
	}
	multiplier, ok := byteSizeUnitMultiplier(unit)
	if !ok {
		return 0, fmt.Errorf("invalid byte size unit %q", unit)
	}
	if number > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("byte size %q is too large", value)
	}
	return number * multiplier, nil
}

// ParseTimeoutSeconds resolves a timeout from seconds or a duration string.
func ParseTimeoutSeconds(seconds int, value string) (int, error) {
	if seconds < 0 {
		return 0, fmt.Errorf("timeout_seconds must be non-negative")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return seconds, nil
	}
	maxInt := int64(^uint(0) >> 1)
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		if parsed <= 0 {
			return 0, fmt.Errorf("timeout must be positive")
		}
		if parsed > maxInt {
			return 0, fmt.Errorf("timeout %q is too large", value)
		}
		return int(parsed), nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse timeout %q: %w", value, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("timeout must be positive")
	}
	parsed := int64(duration / time.Second)
	if duration%time.Second != 0 {
		parsed++
	}
	if parsed > maxInt {
		return 0, fmt.Errorf("timeout %q is too large", value)
	}
	return int(parsed), nil
}

// ValidateControlFreeValue rejects control characters in a config value.
func ValidateControlFreeValue(field, value string) error {
	if containsControlCharacter(value) {
		return fmt.Errorf("%s must not contain control characters", field)
	}
	return nil
}

// ValidateNetworkMode validates a network mode name.
func ValidateNetworkMode(mode string) error {
	switch mode {
	case "none", "default", "allowlist":
		return nil
	default:
		return fmt.Errorf("invalid network mode %q", mode)
	}
}

// EffectiveRunNetworkMode applies the docker-specific network defaults.
func EffectiveRunNetworkMode(networkMode string, docker, networkModeProvided bool) (string, error) {
	if !docker {
		return networkMode, nil
	}
	if !networkModeProvided && networkMode == "default" {
		return "none", nil
	}
	return networkMode, nil
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// ValidateNetworkAllowlistUse checks allowlist/mode consistency.
func ValidateNetworkAllowlistUse(networkMode string, allowlist []string) error {
	if networkMode == "allowlist" && len(allowlist) == 0 {
		return errors.New("network allowlist is required when network mode is allowlist")
	}
	if networkMode != "allowlist" && len(allowlist) > 0 {
		return errors.New("network allowlist requires network mode allowlist")
	}
	if networkMode == "allowlist" {
		if _, err := netpolicy.NormalizeAllowlistEntries(allowlist); err != nil {
			return err
		}
	}
	return nil
}

// MergeResourceDefaults overlays check resource defaults on project defaults.
func MergeResourceDefaults(base, override config.ResourceDefaults) config.ResourceDefaults {
	if override.Size != "" {
		base.Size = override.Size
	}
	if override.Timeout != "" {
		base.Timeout = override.Timeout
	}
	if override.Docker != nil {
		base.Docker = override.Docker
	}
	return base
}

// SortedCheckNames lists configured check names in order.
func SortedCheckNames(checks map[string]config.CheckConfig) []string {
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func byteSizeUnitMultiplier(unit string) (int64, bool) {
	switch unit {
	case "", "b":
		return 1, true
	case "k", "kb":
		return 1000, true
	case "m", "mb":
		return 1000 * 1000, true
	case "g", "gb":
		return 1000 * 1000 * 1000, true
	case "t", "tb":
		return 1000 * 1000 * 1000 * 1000, true
	case "ki", "kib":
		return 1024, true
	case "mi", "mib":
		return 1024 * 1024, true
	case "gi", "gib":
		return 1024 * 1024 * 1024, true
	case "ti", "tib":
		return 1024 * 1024 * 1024 * 1024, true
	default:
		return 0, false
	}
}
