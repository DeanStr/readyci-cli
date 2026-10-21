package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const setupWorkflowMatrixLimit = 64

type setupWorkflowMatrixScalar struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type setupWorkflowMatrixVariant struct {
	Workflow repoWorkflowYAML
	Job      repoWorkflowJobYAML
	Values   map[string]string
	Digest   string
}

// Matrix imports select one concrete upstream job. The original job remains
// blocked so no automatic path silently discards a sibling or shares one
// workspace across independently configured upstream jobs.
func setupWorkflowMatrixVariants(workflow repoWorkflowYAML, job repoWorkflowJobYAML) ([]setupWorkflowMatrixVariant, []string) {
	node := job.StrategyNode
	if node == nil {
		node = &yaml.Node{}
		if err := node.Encode(job.Strategy); err != nil {
			return nil, []string{"matrix strategy cannot be inspected"}
		}
	}
	strategy, err := setupWorkflowNodeMap(node)
	if err != nil {
		return nil, []string{"matrix strategy must be a static mapping"}
	}
	for _, key := range setupWorkflowSortedNodeKeys(strategy) {
		value := strategy[key]
		switch key {
		case "matrix":
		case "fail-fast":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
				return nil, []string{"matrix fail-fast must be a literal boolean"}
			}
		case "max-parallel":
			n, problem := strconv.Atoi(value.Value)
			if value.Kind != yaml.ScalarNode || value.Tag != "!!int" || problem != nil || n < 1 {
				return nil, []string{"matrix max-parallel must be a positive literal integer"}
			}
		default:
			return nil, []string{"matrix strategy field " + key + " requires explicit translation"}
		}
	}
	matrix, err := setupWorkflowNodeMap(strategy["matrix"])
	if err != nil || len(matrix) == 0 {
		return nil, []string{"matrix must be a non-empty static mapping"}
	}
	// Matrix variable names are case-insensitive in GitHub Actions. Reject
	// aliases across the entire declaration before comparing exact map keys;
	// treating an alias as an added field would apply overlays to wrong rows.
	// https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#jobsjob_idstrategymatrix
	variableNames := map[string]string{}
	registerNames := func(names []string) error {
		for _, name := range names {
			folded := strings.ToLower(name)
			if previous, exists := variableNames[folded]; exists && previous != name {
				return fmt.Errorf("matrix variable names are case-insensitive; aliases %s and %s require explicit configuration", previous, name)
			}
			variableNames[folded] = name
		}
		return nil
	}
	var axes []string
	// GitHub uses the declaration order of axes and values when enumerating
	// combinations. Stable variant identities are independent of that order.
	for i := 0; i < len(strategy["matrix"].Content); i += 2 {
		name := strategy["matrix"].Content[i].Value
		if name == "include" || name == "exclude" {
			continue
		}
		if !setupWorkflowMatrixKey.MatchString(name) {
			return nil, []string{"matrix axis names must be static identifiers"}
		}
		if err := registerNames([]string{name}); err != nil {
			return nil, []string{err.Error()}
		}
		axes = append(axes, name)
	}
	if len(axes) == 0 && matrix["include"] != nil && matrix["exclude"] != nil {
		return nil, []string{"include-only matrices combined with exclude require explicit translation"}
	}
	var includes []map[string]setupWorkflowMatrixScalar
	if include := matrix["include"]; include != nil {
		if include.Kind != yaml.SequenceNode || len(include.Content) > setupWorkflowMatrixLimit || len(axes) == 0 && len(include.Content) == 0 {
			return nil, []string{"matrix include must contain at most 64 static rows, and include-only matrices need at least one row"}
		}
		for _, row := range include.Content {
			values, problem := setupWorkflowMatrixRow(row)
			if problem != nil {
				return nil, []string{problem.Error()}
			}
			if err := registerNames(setupWorkflowSortedMatrixKeys(values)); err != nil {
				return nil, []string{err.Error()}
			}
			includes = append(includes, values)
		}
	}
	var rows []map[string]setupWorkflowMatrixScalar
	if len(axes) == 0 {
		if len(includes) == 0 {
			return nil, []string{"matrix needs static axes or include-only rows"}
		}
		rows = includes
	} else {
		values := map[string][]setupWorkflowMatrixScalar{}
		product := 1
		for _, axis := range axes {
			node := matrix[axis]
			if node.Kind != yaml.SequenceNode || len(node.Content) == 0 || len(node.Content) > setupWorkflowMatrixLimit/product {
				return nil, []string{"matrix expansion must contain between 1 and 64 static variants before exclusions"}
			}
			product *= len(node.Content)
			seen := map[setupWorkflowMatrixScalar]bool{}
			for _, item := range node.Content {
				value, problem := setupWorkflowMatrixValue(item)
				if problem != nil {
					return nil, []string{problem.Error()}
				}
				if seen[value] {
					return nil, []string{"duplicate matrix axis values require explicit configuration"}
				}
				seen[value] = true
				values[axis] = append(values[axis], value)
			}
		}
		rows = []map[string]setupWorkflowMatrixScalar{{}}
		for _, axis := range axes {
			var next []map[string]setupWorkflowMatrixScalar
			for _, row := range rows {
				for _, value := range values[axis] {
					copy := make(map[string]setupWorkflowMatrixScalar, len(row)+1)
					for key, existing := range row {
						copy[key] = existing
					}
					copy[axis] = value
					next = append(next, copy)
				}
			}
			rows = next
		}
		if exclude := matrix["exclude"]; exclude != nil {
			if exclude.Kind != yaml.SequenceNode || len(exclude.Content) > setupWorkflowMatrixLimit {
				return nil, []string{"matrix exclude must contain at most 64 static rows"}
			}
			for _, entry := range exclude.Content {
				match, problem := setupWorkflowMatrixRow(entry)
				if problem != nil {
					return nil, []string{problem.Error()}
				}
				if err := registerNames(setupWorkflowSortedMatrixKeys(match)); err != nil {
					return nil, []string{err.Error()}
				}
				for _, key := range setupWorkflowSortedMatrixKeys(match) {
					if _, exists := values[key]; !exists {
						return nil, []string{"matrix exclude references unknown axis " + key}
					}
				}
				var retained []map[string]setupWorkflowMatrixScalar
				for _, row := range rows {
					matches := true
					for key, value := range match {
						matches = matches && row[key] == value
					}
					if !matches {
						retained = append(retained, row)
					}
				}
				rows = retained
			}
		}
		// Exclusions apply before includes. An include may enrich a surviving
		// original combination only when it does not replace an original axis
		// value. Fields introduced by previous includes may be replaced. A row
		// appended by an include is never a target for later include overlays.
		// https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#jobsjob_idstrategymatrixinclude
		originalCount := len(rows)
		for _, include := range includes {
			matched := false
			for _, row := range rows[:originalCount] {
				compatible := true
				for _, axis := range axes {
					if value, exists := include[axis]; exists && row[axis] != value {
						compatible = false
						break
					}
				}
				if !compatible {
					continue
				}
				for key, value := range include {
					row[key] = value
				}
				matched = true
			}
			if !matched {
				rows = append(rows, include)
				if len(rows) > setupWorkflowMatrixLimit {
					return nil, []string{"matrix expansion must contain at most 64 static variants after includes"}
				}
			}
		}
	}
	if len(rows) == 0 {
		return nil, []string{"matrix has no runnable variants after exclusions and includes"}
	}
	seen := map[string]bool{}
	var variants []setupWorkflowMatrixVariant
	for _, row := range rows {
		encoded, _ := json.Marshal(row)
		sum := sha256.Sum256(encoded)
		digest := hex.EncodeToString(sum[:])
		if seen[digest] {
			return nil, []string{"duplicate matrix rows require explicit configuration"}
		}
		seen[digest] = true
		values := map[string]string{}
		for key, value := range row {
			values[key] = value.Value
		}
		typedValues := map[string]any{}
		for key, value := range row {
			switch value.Type {
			case "!!bool":
				typedValues[key] = value.Value == "true"
			case "!!int":
				// Matrix parsing has already bounded integer values. Retain the
				// scalar type for expression truthiness and equality.
				typedValues[key], _ = strconv.ParseFloat(value.Value, 64)
			default:
				typedValues[key] = value.Value
			}
		}
		concreteWorkflow, concrete := setupWorkflowStaticJob(workflow, job, typedValues)
		variants = append(variants, setupWorkflowMatrixVariant{Workflow: concreteWorkflow, Job: concrete, Values: values, Digest: digest})
	}
	return variants, nil
}

var setupWorkflowMatrixKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func setupWorkflowMatrixValue(node *yaml.Node) (setupWorkflowMatrixScalar, error) {
	if node == nil || node.Kind != yaml.ScalarNode || strings.Contains(node.Value, "${{") || strings.ContainsAny(node.Value, "\x00\r\n") {
		return setupWorkflowMatrixScalar{}, fmt.Errorf("matrix values must be literal single-line scalar values")
	}
	switch node.Tag {
	case "!!str":
		return setupWorkflowMatrixScalar{Type: node.Tag, Value: node.Value}, nil
	case "!!int", "!!bool":
		var decoded any
		if err := node.Decode(&decoded); err != nil {
			return setupWorkflowMatrixScalar{}, fmt.Errorf("matrix scalar cannot be decoded")
		}
		switch decoded.(type) {
		case int, int64, uint64, bool:
		default:
			return setupWorkflowMatrixScalar{}, fmt.Errorf("matrix integer is outside the supported range; use a quoted string")
		}
		return setupWorkflowMatrixScalar{Type: node.Tag, Value: fmt.Sprint(decoded)}, nil
	case "!!float":
		return setupWorkflowMatrixScalar{}, fmt.Errorf("matrix floating-point values require quoted strings to preserve their intended spelling")
	default:
		return setupWorkflowMatrixScalar{}, fmt.Errorf("matrix values must be strings, integers, or booleans")
	}
}

func setupWorkflowMatrixRow(node *yaml.Node) (map[string]setupWorkflowMatrixScalar, error) {
	fields, err := setupWorkflowNodeMap(node)
	if err != nil || len(fields) == 0 {
		return nil, fmt.Errorf("matrix rows must be non-empty static scalar mappings")
	}
	out := map[string]setupWorkflowMatrixScalar{}
	for _, key := range setupWorkflowSortedNodeKeys(fields) {
		value := fields[key]
		if !setupWorkflowMatrixKey.MatchString(key) {
			return nil, fmt.Errorf("matrix row keys must be static identifiers")
		}
		scalar, err := setupWorkflowMatrixValue(value)
		if err != nil {
			return nil, err
		}
		out[key] = scalar
	}
	return out, nil
}

func setupWorkflowNodeMap(node *yaml.Node) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected a mapping")
	}
	out := map[string]*yaml.Node{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("mapping keys must be literal strings")
		}
		if _, exists := out[key.Value]; exists {
			return nil, fmt.Errorf("duplicate mapping key")
		}
		out[key.Value] = node.Content[i+1]
	}
	return out, nil
}

func setupWorkflowSortedNodeKeys(fields map[string]*yaml.Node) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func setupWorkflowSortedMatrixKeys(fields map[string]setupWorkflowMatrixScalar) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func setupWorkflowUnknownFields(node *yaml.Node, scope string, allowed ...string) []string {
	fields, err := setupWorkflowNodeMap(node)
	if err != nil {
		return []string{scope + " must be a static mapping"}
	}
	known := map[string]bool{}
	for _, key := range allowed {
		known[key] = true
	}
	var problems []string
	for key := range fields {
		if !known[key] {
			problems = append(problems, scope+" field "+key+" requires explicit translation")
		}
	}
	sort.Strings(problems)
	return problems
}

func setupWorkflowDefaultProblems(node *yaml.Node) []string {
	fields, err := setupWorkflowNodeMap(node)
	if err != nil || fields["defaults"] == nil {
		return nil
	}
	problems := setupWorkflowUnknownFields(fields["defaults"], "defaults", "run")
	defaults, err := setupWorkflowNodeMap(fields["defaults"])
	if err == nil && defaults["run"] != nil {
		problems = append(problems, setupWorkflowUnknownFields(defaults["run"], "defaults.run", "shell", "working-directory")...)
	}
	return problems
}
