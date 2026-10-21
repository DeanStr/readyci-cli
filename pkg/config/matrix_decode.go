package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func (matrix MatrixConfig) Empty() bool {
	return len(matrix.Axes) == 0 && len(matrix.Include) == 0 && len(matrix.Exclude) == 0 && !matrix.FailFast && matrix.MaxParallel == 0
}

func (matrix *MatrixConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode && value.Tag == "!!null" {
		*matrix = MatrixConfig{}
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("expected matrix object")
	}
	if !matrixUsesStructuredForm(value) {
		axes, err := decodeMatrixAxes(value)
		if err != nil {
			return err
		}
		*matrix = MatrixConfig{Axes: axes}
		return nil
	}
	var out MatrixConfig
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i].Value
		field := value.Content[i+1]
		switch key {
		case "axes":
			axes, err := decodeMatrixAxes(field)
			if err != nil {
				return fmt.Errorf("matrix.axes: %w", err)
			}
			out.Axes = axes
		case "include":
			entries, err := decodeMatrixEntries(field)
			if err != nil {
				return fmt.Errorf("matrix.include: %w", err)
			}
			out.Include = entries
		case "exclude":
			entries, err := decodeMatrixEntries(field)
			if err != nil {
				return fmt.Errorf("matrix.exclude: %w", err)
			}
			out.Exclude = entries
		case "fail_fast":
			if err := field.Decode(&out.FailFast); err != nil {
				return fmt.Errorf("matrix.fail_fast: %w", err)
			}
		case "max_parallel":
			if err := field.Decode(&out.MaxParallel); err != nil {
				return fmt.Errorf("matrix.max_parallel: %w", err)
			}
		default:
			return fmt.Errorf("field %s not found in type config.MatrixConfig", key)
		}
	}
	*matrix = out
	return nil
}

func matrixUsesStructuredForm(value *yaml.Node) bool {
	if value.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		switch value.Content[i].Value {
		case "axes":
			if value.Content[i+1].Kind != yaml.SequenceNode {
				return true
			}
		case "fail_fast", "max_parallel":
			if value.Content[i+1].Kind != yaml.SequenceNode {
				return true
			}
		case "include", "exclude":
			if matrixIncludeExcludeValueIsStructured(value.Content[i+1]) {
				return true
			}
		}
	}
	return false
}

func matrixIncludeExcludeValueIsStructured(value *yaml.Node) bool {
	if value.Kind != yaml.SequenceNode {
		return true
	}
	if len(value.Content) == 0 {
		return true
	}
	for _, item := range value.Content {
		if item.Kind == yaml.MappingNode {
			return true
		}
	}
	return false
}

func decodeMatrixAxes(value *yaml.Node) (map[string][]string, error) {
	if value.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected object")
	}
	axes := make(map[string][]string, len(value.Content)/2)
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i].Value
		field := value.Content[i+1]
		if field.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("%s must be a list", key)
		}
		values := make([]string, 0, len(field.Content))
		for _, item := range field.Content {
			itemValue, err := decodeMatrixScalar(item)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			values = append(values, itemValue)
		}
		axes[key] = values
	}
	return axes, nil
}

func decodeMatrixEntries(value *yaml.Node) ([]map[string]string, error) {
	if value.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("expected list")
	}
	entries := make([]map[string]string, 0, len(value.Content))
	for index, item := range value.Content {
		if item.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("[%d] must be an object", index)
		}
		entry := make(map[string]string, len(item.Content)/2)
		for i := 0; i+1 < len(item.Content); i += 2 {
			key := item.Content[i].Value
			value, err := decodeMatrixScalar(item.Content[i+1])
			if err != nil {
				return nil, fmt.Errorf("[%d].%s: %w", index, key, err)
			}
			entry[key] = value
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func decodeMatrixScalar(value *yaml.Node) (string, error) {
	if value.Kind != yaml.ScalarNode || value.Tag == "!!null" {
		return "", fmt.Errorf("must be a scalar value")
	}
	return strings.TrimSpace(value.Value), nil
}

func (commands *CommandList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var command string
		if err := value.Decode(&command); err != nil {
			return err
		}
		if command != "" {
			*commands = []string{command}
		}
		return nil
	case yaml.SequenceNode:
		var values []string
		if err := value.Decode(&values); err != nil {
			return err
		}
		*commands = values
		return nil
	default:
		return fmt.Errorf("expected command string or list")
	}
}

func (cfg *CheckConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		var command string
		if err := value.Decode(&command); err != nil {
			return err
		}
		cfg.Command = command
		return nil
	}
	if err := validateCheckKnownFields(value); err != nil {
		return err
	}
	retriesSet := mappingHasKey(value, "retries")
	concurrencyGroupSet := mappingHasKey(value, "concurrency_group")
	concurrencyLimitSet := mappingHasKey(value, "concurrency_limit")
	type rawCheckConfig CheckConfig
	var raw rawCheckConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*cfg = CheckConfig(raw)
	cfg.RetriesSet = retriesSet
	cfg.ConcurrencyGroupSet = concurrencyGroupSet
	cfg.ConcurrencyLimitSet = concurrencyLimitSet
	return nil
}

func mappingHasKey(value *yaml.Node, key string) bool {
	if value.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(value.Content); i += 2 {
		if value.Content[i].Value == key {
			return true
		}
	}
	return false
}
