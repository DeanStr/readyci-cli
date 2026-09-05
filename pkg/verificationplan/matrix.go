package verificationplan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

var matrixEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ExpandMatrix expands a matrix into its concrete variants, applying exclude
// and include entries. A matrix with no axes yields a single unnamed variant.
// Variant is one concrete point in an expanded matrix.
type Variant struct {
	Name string
	Env  map[string]string
}

func ExpandMatrix(matrix config.MatrixConfig) ([]Variant, error) {
	if matrix.MaxParallel < 0 {
		return nil, fmt.Errorf("matrix.max_parallel must be non-negative")
	}
	if len(matrix.Axes) == 0 && len(matrix.Include) == 0 && len(matrix.Exclude) == 0 {
		return []Variant{{}}, nil
	}
	keys := make([]string, 0, len(matrix.Axes))
	for key := range matrix.Axes {
		if err := validateVariable(key); err != nil {
			return nil, err
		}
		values := matrix.Axes[key]
		if len(values) == 0 {
			return nil, fmt.Errorf("matrix variable %s has no values", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var variants []Variant
	if len(keys) > 0 {
		var walk func(int, map[string]string, []string)
		var walkErr error
		walk = func(index int, env map[string]string, labels []string) {
			if walkErr != nil {
				return
			}
			if index == len(keys) {
				copiedEnv := make(map[string]string, len(env))
				for key, value := range env {
					copiedEnv[key] = value
				}
				variants = append(variants, Variant{Name: strings.Join(labels, ","), Env: copiedEnv})
				return
			}
			key := keys[index]
			for _, value := range matrix.Axes[key] {
				if err := validateValue(key, value); err != nil {
					walkErr = err
					return
				}
				value = strings.TrimSpace(value)
				nextEnv := make(map[string]string, len(env)+1)
				for existingKey, existingValue := range env {
					nextEnv[existingKey] = existingValue
				}
				nextEnv[key] = value
				walk(index+1, nextEnv, append(labels, key+"="+value))
			}
		}
		walk(0, map[string]string{}, nil)
		if walkErr != nil {
			return nil, walkErr
		}
	}
	var err error
	variants, err = excludeVariants(variants, matrix.Exclude)
	if err != nil {
		return nil, err
	}
	variants, err = includeVariants(variants, matrix.Include, keys)
	if err != nil {
		return nil, err
	}
	if len(variants) == 0 && len(keys) == 0 && len(matrix.Include) == 0 && len(matrix.Exclude) == 0 {
		return []Variant{{}}, nil
	}
	return variants, nil
}

func validateVariable(key string) error {
	normalized := strings.TrimSpace(key)
	if normalized == "" || normalized != key || !matrixEnvNamePattern.MatchString(normalized) {
		return fmt.Errorf("invalid matrix variable %q", key)
	}
	if strings.HasPrefix(strings.ToLower(normalized), "__readyci_") {
		return fmt.Errorf("matrix variable %q uses reserved Ready CI prefix __readyci_", key)
	}
	return nil
}

func validateValue(key, value string) error {
	if containsControlCharacter(value) {
		return fmt.Errorf("matrix variable %s value must not contain control characters", key)
	}
	return nil
}

func excludeVariants(variants []Variant, excludes []map[string]string) ([]Variant, error) {
	if len(excludes) == 0 {
		return variants, nil
	}
	for _, exclude := range excludes {
		if err := validateEntry(exclude); err != nil {
			return nil, fmt.Errorf("matrix.exclude: %w", err)
		}
	}
	filtered := variants[:0]
	for _, variant := range variants {
		excluded := false
		for _, exclude := range excludes {
			if entryMatches(variant.Env, exclude) {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, variant)
		}
	}
	return filtered, nil
}

func includeVariants(variants []Variant, includes []map[string]string, axisKeys []string) ([]Variant, error) {
	if len(includes) == 0 {
		return variants, nil
	}
	axisKeySet := make(map[string]struct{}, len(axisKeys))
	for _, key := range axisKeys {
		axisKeySet[key] = struct{}{}
	}
	for _, include := range includes {
		if err := validateEntry(include); err != nil {
			return nil, fmt.Errorf("matrix.include: %w", err)
		}
		matched := false
		for index := range variants {
			if !includeCompatible(variants[index].Env, include, axisKeySet) {
				continue
			}
			variants[index].Env = mergeStringMaps(variants[index].Env, include)
			variants[index].Name = VariantName(variants[index].Env)
			matched = true
		}
		if !matched {
			env := mergeStringMaps(nil, include)
			variants = append(variants, Variant{Name: VariantName(env), Env: env})
		}
	}
	return variants, nil
}

func validateEntry(entry map[string]string) error {
	if len(entry) == 0 {
		return fmt.Errorf("entry cannot be empty")
	}
	for key, value := range entry {
		if err := validateVariable(key); err != nil {
			return err
		}
		if err := validateValue(key, value); err != nil {
			return err
		}
	}
	return nil
}

func entryMatches(env map[string]string, entry map[string]string) bool {
	for key, value := range entry {
		if env[key] != strings.TrimSpace(value) {
			return false
		}
	}
	return true
}

func includeCompatible(env map[string]string, include map[string]string, axisKeys map[string]struct{}) bool {
	for key, value := range include {
		if _, ok := axisKeys[key]; !ok {
			continue
		}
		if env[key] != strings.TrimSpace(value) {
			return false
		}
	}
	return len(env) > 0
}

func VariantName(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	labels := make([]string, 0, len(keys))
	for _, key := range keys {
		labels = append(labels, key+"="+strings.TrimSpace(env[key]))
	}
	return strings.Join(labels, ",")
}

func mergeStringMaps(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

func containsControlCharacter(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
