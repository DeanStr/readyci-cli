package main

import (
	"reflect"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"gopkg.in/yaml.v3"
)

func TestKnownAffectedProfilesAreSortedAndIncludeBuiltIns(t *testing.T) {
	profiles := knownAffectedProfiles()
	if len(profiles) == 0 {
		t.Fatal("known profiles is empty")
	}
	for _, want := range []string{"docs", "go", "readyci", "shell"} {
		if !contains(profiles, want) {
			t.Fatalf("known profiles = %v, want %s baseline profile", profiles, want)
		}
	}
	for _, notWant := range []string{"vite-react", "go-postgres", "rust-workspace", "openapi"} {
		if contains(profiles, notWant) {
			t.Fatalf("known profiles = %v, should not include hosted profile %s", profiles, notWant)
		}
	}
	for i := 1; i < len(profiles); i++ {
		if profiles[i-1] > profiles[i] {
			t.Fatalf("known profiles are not sorted: %v", profiles)
		}
	}
}

func affectedRulesContainCheck(rules []config.AffectedRule, check string) bool {
	for _, rule := range rules {
		for _, candidate := range rule.Checks {
			if candidate == check {
				return true
			}
		}
	}
	return false
}

func TestPruneSubsumedAffectedChecksWrapper(t *testing.T) {
	cfg := config.ProjectConfig{
		Affected: config.AffectedConfig{Subsumes: map[string]config.CommandList{"full": {"unit", "lint"}}},
	}
	reasons := map[string][]affectedRuleMatch{
		"full": {{Rule: 0, Pattern: "src/**", Path: "src/app.go"}},
		"unit": {{Rule: 1, Pattern: "src/**", Path: "src/app.go"}},
		"lint": {{Rule: 2, Pattern: "src/**", Path: "src/app.go"}},
	}
	got := pruneSubsumedAffectedChecks(cfg, []string{"full", "unit", "lint"}, reasons)
	if !reflect.DeepEqual(got, []string{"full"}) {
		t.Fatalf("pruned checks = %v, want only full", got)
	}
}

func TestAffectedTestsIncludeExactPathNormalizesSingularAndPluralPaths(t *testing.T) {
	tests := []config.AffectedTest{
		{Path: " apps/web/src/App.tsx ", Checks: config.CommandList{"web-test"}},
		{Paths: []string{"packages/ui/src/button.tsx", "./docs/guide.md"}, Checks: config.CommandList{"ui-test"}},
	}

	for _, file := range []string{
		"apps/web/src/App.tsx",
		"./packages/ui/src/button.tsx",
		"docs/guide.md",
	} {
		if !affectedTestsIncludeExactPath(tests, file) {
			t.Fatalf("affectedTestsIncludeExactPath(%q) = false, want true", file)
		}
	}
	if affectedTestsIncludeExactPath(tests, "apps/web/src/Other.tsx") {
		t.Fatal("affectedTestsIncludeExactPath matched a file not listed exactly")
	}
}

func TestYAMLFixHelpersNormalizeNodes(t *testing.T) {
	var doc yaml.Node
	root := ensureYAMLDocumentMapping(&doc)
	if doc.Kind != yaml.DocumentNode || root.Kind != yaml.MappingNode {
		t.Fatalf("empty doc/root = %v/%v, want document mapping", doc.Kind, root.Kind)
	}
	root.Content = append(root.Content, yamlScalarNode("affected"), yamlScalarNode("not a map"))
	affected := ensureYAMLMappingChild(root, "affected")
	if affected.Kind != yaml.MappingNode || affected.Value != "" || len(affected.Content) != 0 {
		t.Fatalf("affected mapping = %+v, want reset map", affected)
	}
	affected.Content = append(affected.Content, yamlScalarNode("tests"), yamlScalarNode("not a sequence"))
	tests := ensureYAMLSequenceChild(affected, "tests")
	if tests.Kind != yaml.SequenceNode || tests.Value != "" || len(tests.Content) != 0 {
		t.Fatalf("tests sequence = %+v, want reset sequence", tests)
	}

	tests.Content = append(tests.Content, yamlScalarNode("apps/web/**"), &yaml.Node{Kind: yaml.MappingNode})
	set := yamlStringSequenceSet(tests)
	if len(set) != 1 || !set["apps/web/**"] {
		t.Fatalf("sequence set = %v, want scalar value only", set)
	}
	if got := yamlStringSequenceSet(nil); len(got) != 0 {
		t.Fatalf("nil sequence set = %v, want empty", got)
	}

	testNode := yamlAffectedTestNode([]string{"apps/web/**", "packages/ui/**"}, []string{"web", "ui"})
	if testNode.Kind != yaml.MappingNode || len(testNode.Content) != 4 {
		t.Fatalf("affected test node = %+v", testNode)
	}
	if child := yamlMappingChild(testNode, "checks"); child == nil || child.Kind != yaml.SequenceNode || len(child.Content) != 2 {
		t.Fatalf("checks child = %+v, want two checks", child)
	}
	if child := yamlMappingChild(&yaml.Node{Kind: yaml.SequenceNode}, "checks"); child != nil {
		t.Fatalf("non-mapping child = %+v, want nil", child)
	}
}
