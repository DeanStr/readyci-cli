package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type affectedAuditFixReport struct {
	ConfigPath          string `json:"config_path"`
	AddedTests          int    `json:"added_tests"`
	AddedAuditIgnores   int    `json:"added_audit_ignores"`
	Updated             bool   `json:"updated"`
	UnmatchedNotIgnored int    `json:"unmatched_not_ignored"`
}

func applyAffectedAuditFix(configPath string, report affectedAuditReport, ignoreUnmatched bool) (affectedAuditFixReport, error) {
	info, err := os.Stat(configPath)
	if err != nil {
		return affectedAuditFixReport{}, err
	}
	mode := info.Mode().Perm()
	data, err := os.ReadFile(configPath)
	if err != nil {
		return affectedAuditFixReport{}, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return affectedAuditFixReport{}, fmt.Errorf("parse %s: %w", configPath, err)
	}
	root := ensureYAMLDocumentMapping(&doc)
	affected := ensureYAMLMappingChild(root, "affected")
	fixReport := affectedAuditFixReport{ConfigPath: configPath}
	if len(report.Examples) > 0 {
		tests := ensureYAMLSequenceChild(affected, "tests")
		for _, example := range report.Examples {
			tests.Content = append(tests.Content, yamlAffectedTestNode(example.Paths, example.Checks))
			fixReport.AddedTests++
		}
	}
	if ignoreUnmatched && len(report.UnmatchedFiles) > 0 {
		auditIgnore := ensureYAMLSequenceChild(affected, "audit_ignore")
		existing := yamlStringSequenceSet(auditIgnore)
		for _, file := range report.UnmatchedFiles {
			if existing[file] {
				continue
			}
			auditIgnore.Content = append(auditIgnore.Content, yamlScalarNode(file))
			existing[file] = true
			fixReport.AddedAuditIgnores++
		}
	} else {
		fixReport.UnmatchedNotIgnored = len(report.UnmatchedFiles)
	}
	fixReport.Updated = fixReport.AddedTests > 0 || fixReport.AddedAuditIgnores > 0
	if !fixReport.Updated {
		return fixReport, nil
	}
	var out strings.Builder
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		_ = encoder.Close()
		return affectedAuditFixReport{}, err
	}
	if err := encoder.Close(); err != nil {
		return affectedAuditFixReport{}, err
	}
	if err := os.WriteFile(configPath, []byte(out.String()), mode); err != nil {
		return affectedAuditFixReport{}, err
	}
	return fixReport, nil
}

func ensureYAMLDocumentMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if doc.Kind != yaml.DocumentNode {
		original := *doc
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{&original}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	return doc.Content[0]
}

func ensureYAMLMappingChild(mapping *yaml.Node, key string) *yaml.Node {
	if child := yamlMappingChild(mapping, key); child != nil {
		if child.Kind != yaml.MappingNode {
			child.Kind = yaml.MappingNode
			child.Tag = "!!map"
			child.Value = ""
			child.Content = nil
		}
		return child
	}
	keyNode := yamlScalarNode(key)
	valueNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, keyNode, valueNode)
	return valueNode
}

func ensureYAMLSequenceChild(mapping *yaml.Node, key string) *yaml.Node {
	if child := yamlMappingChild(mapping, key); child != nil {
		if child.Kind != yaml.SequenceNode {
			child.Kind = yaml.SequenceNode
			child.Tag = "!!seq"
			child.Value = ""
			child.Content = nil
		}
		return child
	}
	keyNode := yamlScalarNode(key)
	valueNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	mapping.Content = append(mapping.Content, keyNode, valueNode)
	return valueNode
}

func yamlMappingChild(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func yamlScalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func yamlStringSequenceNode(values []string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		node.Content = append(node.Content, yamlScalarNode(value))
	}
	return node
}

func yamlAffectedTestNode(paths []string, checks []string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if len(paths) == 1 {
		node.Content = append(node.Content, yamlScalarNode("path"), yamlScalarNode(paths[0]))
	} else {
		node.Content = append(node.Content, yamlScalarNode("paths"), yamlStringSequenceNode(paths))
	}
	node.Content = append(node.Content, yamlScalarNode("checks"), yamlStringSequenceNode(checks))
	return node
}

func yamlStringSequenceSet(node *yaml.Node) map[string]bool {
	values := make(map[string]bool)
	if node == nil || node.Kind != yaml.SequenceNode {
		return values
	}
	for _, item := range node.Content {
		if item.Kind == yaml.ScalarNode {
			values[item.Value] = true
		}
	}
	return values
}

func printAffectedAuditFixReport(report affectedAuditFixReport) {
	fmt.Println("affected: audit-fix")
	fmt.Printf("config: %s\n", report.ConfigPath)
	fmt.Printf("updated: %t\n", report.Updated)
	fmt.Printf("added_tests: %d\n", report.AddedTests)
	fmt.Printf("added_audit_ignores: %d\n", report.AddedAuditIgnores)
	if report.UnmatchedNotIgnored > 0 {
		fmt.Printf("unmatched_not_ignored: %d\n", report.UnmatchedNotIgnored)
		fmt.Println("hint: rerun with --fix --fix-ignore-unmatched only for files intentionally outside affected coverage")
	}
}
