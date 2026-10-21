package config

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// Node.Decode does not inherit the outer decoder's KnownFields setting. Check
// shorthand needs custom decoding, so validate its typed subtree without
// serializing away aliases to anchors elsewhere in the original document.
func validateCheckKnownFields(value *yaml.Node) error {
	validator := checkFieldValidator{
		checked:   make(map[checkTypedNode]bool),
		mappings:  make(map[*yaml.Node][]checkYAMLField),
		expanding: make(map[*yaml.Node]bool),
		remaining: 1 << 16,
	}
	return validator.validate(value, reflect.TypeFor[CheckConfig]())
}

type checkTypedNode struct {
	node *yaml.Node
	kind reflect.Type
}
type checkYAMLField struct {
	name  string
	value *yaml.Node
}
type checkFieldValidator struct {
	checked   map[checkTypedNode]bool
	mappings  map[*yaml.Node][]checkYAMLField
	expanding map[*yaml.Node]bool
	remaining int
}

func (validator *checkFieldValidator) validate(value *yaml.Node, kind reflect.Type) error {
	for kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	// These custom descendants own their accepted shorthand/structured forms.
	if kind == reflect.TypeFor[MatrixConfig]() || kind == reflect.TypeFor[CommandList]() {
		return nil
	}
	var err error
	value, err = checkAliasTarget(value)
	if err != nil {
		return err
	}
	if value == nil {
		return nil
	}
	key := checkTypedNode{value, kind}
	if validator.checked[key] {
		return nil
	}
	if err := validator.spend(); err != nil {
		return err
	}
	validator.checked[key] = true
	switch kind.Kind() {
	case reflect.Struct:
		if value.Kind != yaml.MappingNode {
			return nil
		} // The decoder owns shape/type errors.
		fields, err := validator.mapping(value)
		if err != nil {
			return err
		}
		known := make(map[string]reflect.Type, kind.NumField())
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("yaml"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = strings.ToLower(field.Name)
			}
			known[name] = field.Type
		}
		for _, field := range fields {
			fieldType, ok := known[field.name]
			if !ok {
				return fmt.Errorf("field %s not found in type %s", field.name, kind)
			}
			if err := validator.validate(field.value, fieldType); err != nil {
				return err
			}
		}
	case reflect.Map:
		if value.Kind != yaml.MappingNode {
			return nil
		}
		fields, err := validator.mapping(value)
		if err != nil {
			return err
		}
		for _, field := range fields {
			if err := validator.validate(field.value, kind.Elem()); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if value.Kind != yaml.SequenceNode {
			return nil
		}
		for _, item := range value.Content {
			if err := validator.validate(item, kind.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkAliasTarget(value *yaml.Node) (*yaml.Node, error) {
	seen := map[*yaml.Node]bool{}
	for value != nil && value.Kind == yaml.AliasNode {
		if seen[value] {
			return nil, fmt.Errorf("yaml alias cycle in check configuration")
		}
		seen[value] = true
		value = value.Alias
	}
	return value, nil
}

// Resolve effective mapping entries as yaml.v3 does: explicit fields override
// merges regardless of position, and earlier maps in a merge sequence win.
// Memoizing by node bounds repeated alias expansion; typed validation above is
// separately keyed by node AND destination type so freeform maps cannot mask
// an unknown field when the same anchor is used as a configuration object.
func (validator *checkFieldValidator) mapping(value *yaml.Node) ([]checkYAMLField, error) {
	var err error
	value, err = checkAliasTarget(value)
	if err != nil {
		return nil, err
	}
	if value == nil || value.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("yaml merge in check configuration requires a mapping")
	}
	if fields, ok := validator.mappings[value]; ok {
		return fields, nil
	}
	if validator.expanding[value] {
		return nil, fmt.Errorf("yaml alias cycle in check configuration")
	}
	if err := validator.spend(); err != nil {
		return nil, err
	}
	validator.expanding[value] = true
	defer delete(validator.expanding, value)
	var fields []checkYAMLField
	seen := map[string]bool{}
	var merges []*yaml.Node
	for i := 0; i+1 < len(value.Content); i += 2 {
		originalKey := value.Content[i]
		if originalKey.Kind == yaml.ScalarNode && originalKey.Value == "<<" && (originalKey.Tag == "!!merge" || originalKey.Tag == "" || originalKey.Tag == "!") {
			merges = append(merges, value.Content[i+1])
			continue
		}
		key, err := checkAliasTarget(originalKey)
		if err != nil {
			return nil, err
		}
		if key == nil {
			continue
		}
		if err := validator.spend(); err != nil {
			return nil, err
		}
		fields = append(fields, checkYAMLField{key.Value, value.Content[i+1]})
		seen[key.Value] = true
	}
	for _, merge := range merges {
		target, err := checkAliasTarget(merge)
		if err != nil {
			return nil, err
		}
		sources := []*yaml.Node{target}
		if target != nil && target.Kind == yaml.SequenceNode {
			sources = target.Content
		}
		for _, source := range sources {
			inherited, err := validator.mapping(source)
			if err != nil {
				return nil, err
			}
			for _, field := range inherited {
				if !seen[field.name] {
					if err := validator.spend(); err != nil {
						return nil, err
					}
					fields = append(fields, field)
					seen[field.name] = true
				}
			}
		}
	}
	validator.mappings[value] = fields
	return fields, nil
}

// Bound both traversal and retained flattened merge entries before allocating.
// Memoization alone does not bound growing prefixes in a nested merge chain.
func (validator *checkFieldValidator) spend() error {
	if validator.remaining == 0 {
		return fmt.Errorf("check configuration field validation exceeds complexity limit")
	}
	validator.remaining--
	return nil
}
