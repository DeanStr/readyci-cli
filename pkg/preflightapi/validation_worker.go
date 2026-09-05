package preflightapi

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

func NormalizeWorkerLabels(labels map[string]string) (map[string]string, error) {
	return normalizeWorkerLabels(labels, false)
}

func NormalizeWorkerSelector(selector map[string]string) (map[string]string, error) {
	return normalizeWorkerLabels(selector, true)
}

func normalizeWorkerLabels(labels map[string]string, allowSystemLabels bool) (map[string]string, error) {
	if len(labels) == 0 {
		return nil, nil
	}
	if len(labels) > MaxWorkerLabels {
		return nil, fmt.Errorf("worker labels must contain at most %d entries", MaxWorkerLabels)
	}
	normalized := make(map[string]string, len(labels))
	for key, value := range labels {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		var err error
		if allowSystemLabels && isKnownWorkerSystemLabel(key) {
			err = validateKnownWorkerSystemLabel(key, value)
		} else {
			err = ValidateWorkerLabel(key, value)
		}
		if err != nil {
			return nil, err
		}
		if _, ok := normalized[key]; ok {
			return nil, fmt.Errorf("label key %q is duplicated after trimming whitespace", key)
		}
		normalized[key] = value
	}
	return normalized, nil
}

func ValidateWorkerLabels(labels map[string]string) error {
	_, err := NormalizeWorkerLabels(labels)
	return err
}

func NormalizeWorkerClaimLabels(labels map[string]string) (map[string]string, error) {
	if len(labels) == 0 {
		return nil, nil
	}
	userLabels := make(map[string]string, len(labels))
	systemLabels := make(map[string]string, 5)
	for rawKey, rawValue := range labels {
		key := strings.TrimSpace(rawKey)
		value := strings.TrimSpace(rawValue)
		switch key {
		case WorkerIDSystemLabel, RootFSReleaseSystemLabel, RuntimeManifestSystemLabel, ApprovedRuntimeSystemLabel, WorkerReleaseSystemLabel:
			if _, exists := systemLabels[key]; exists {
				return nil, fmt.Errorf("label key %q is duplicated after trimming whitespace", key)
			}
			if err := validateKnownWorkerSystemLabel(key, value); err != nil {
				return nil, err
			}
			systemLabels[key] = value
		default:
			userLabels[rawKey] = rawValue
		}
	}
	normalized, err := NormalizeWorkerLabels(userLabels)
	if err != nil {
		return nil, err
	}
	if normalized == nil {
		normalized = make(map[string]string, len(systemLabels))
	}
	for key, value := range systemLabels {
		normalized[key] = value
	}
	return normalized, nil
}

func validLowerHexDigest(value string, size int) bool {
	if len(value) != size*2 {
		return false
	}
	for _, char := range value {
		if char >= '0' && char <= '9' || char >= 'a' && char <= 'f' {
			continue
		}
		return false
	}
	return true
}

func ValidateWorkerSelector(selector map[string]string) error {
	if _, err := NormalizeWorkerSelector(selector); err != nil {
		return fmt.Errorf("worker_selector %w", err)
	}
	return nil
}

func ValidateWorkerLabel(key, value string) error {
	return validateWorkerLabel(key, value, false)
}

func validateKnownWorkerSystemLabel(key, value string) error {
	if !isKnownWorkerSystemLabel(key) {
		return fmt.Errorf("label key %q is not a recognized system label", key)
	}
	if err := validateWorkerLabel(key, value, true); err != nil {
		return err
	}
	if (key == RootFSReleaseSystemLabel || key == RuntimeManifestSystemLabel || key == ApprovedRuntimeSystemLabel || key == WorkerReleaseSystemLabel) && !validLowerHexDigest(value, sha256.Size) {
		return fmt.Errorf("label %q must be a lowercase SHA-256 digest", key)
	}
	if key == DockerCapabilitySystemLabel && value != "true" {
		return fmt.Errorf("label %q must be true", key)
	}
	return nil
}

func isKnownWorkerSystemLabel(key string) bool {
	return key == WorkerIDSystemLabel || key == RootFSReleaseSystemLabel || key == RuntimeManifestSystemLabel || key == ApprovedRuntimeSystemLabel || key == WorkerReleaseSystemLabel || key == DockerCapabilitySystemLabel
}

func validateWorkerLabel(key, value string, allowSystemLabel bool) error {
	if key == "" {
		return fmt.Errorf("label key is required")
	}
	if len(key) > MaxWorkerLabelKeyLength {
		return fmt.Errorf("label key %q is too long", key)
	}
	if !allowSystemLabel && (strings.HasPrefix(key, ReservedWorkerLabelPrefix) || strings.HasPrefix(key, ReservedWorkerSystemLabelPrefix)) {
		return fmt.Errorf("label key %q uses reserved prefix \"readyci\"", key)
	}
	if value == "" {
		return fmt.Errorf("label %q value is required", key)
	}
	if len(value) > MaxWorkerLabelValueLength {
		return fmt.Errorf("label %q value is too long", key)
	}
	if !validWorkerLabelToken(key, true) {
		return fmt.Errorf("label key %q is invalid", key)
	}
	if !validWorkerLabelToken(value, false) {
		return fmt.Errorf("label %q value is invalid", key)
	}
	return nil
}

func validWorkerLabelToken(value string, key bool) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		case !key && r == '/':
		default:
			return false
		}
	}
	return true
}

func ValidRunServiceEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for index, r := range key {
		if r == '_' || r >= 'A' && r <= 'Z' {
			continue
		}
		if index > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func SecretBearingRunServiceEnv(key, value string) bool {
	key = strings.ToLower(key)
	value = strings.ToLower(strings.Join(strings.Fields(value), ""))
	return strings.Contains(key, "from_secret") ||
		strings.Contains(value, "${{secrets.") ||
		strings.Contains(value, "${{secrets[") ||
		strings.Contains(value, "from_secret")
}
