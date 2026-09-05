package verificationplan

import (
	"reflect"
	"sort"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

// Rule order determines selection order, so the rule lists checks in a fixed
// order rather than whatever the map yields.
func serviceConfigForTest(checks map[string]config.CheckConfig) config.ProjectConfig {
	names := make([]string, 0, len(checks))
	for name := range checks {
		names = append(names, name)
	}
	sort.Strings(names)
	return config.ProjectConfig{
		Checks: checks,
		Affected: config.AffectedConfig{Rules: []config.AffectedRule{
			{Paths: []string{"src/**"}, Checks: names},
		}},
	}
}

func serviceChangedFiles(cfg config.ProjectConfig, files ...string) map[string][]string {
	changed := make(map[string][]string, len(cfg.Checks))
	for name := range cfg.Checks {
		changed[name] = append([]string(nil), files...)
	}
	return changed
}

// Bundle branch metadata is caller-supplied and not bound by the workspace
// digest, so it cannot select a service-authorized check.
func TestServicePlanRejectsManifestBranchAsAuthority(t *testing.T) {
	cfg := serviceConfigForTest(map[string]config.CheckConfig{
		"unit":     {},
		"security": {When: config.WhenConfig{Branch: []string{"main"}}},
	})
	changed := serviceChangedFiles(cfg, "src/app.go")

	selected, _, skipped, err := SelectChecksForServicePlan(cfg, changed, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []string{"unit"}) {
		t.Fatalf("selected = %v, want only the unconditional check", selected)
	}
	if len(skipped) != 1 || skipped[0].Name != "security" ||
		skipped[0].Reason != "not service-verifiable: when.branch" {
		t.Fatalf("skipped = %+v, want branch provenance gap", skipped)
	}
}

// A check gated on something the service cannot derive is neither silently run
// nor silently dropped: it is reported with the paths it would have covered, so
// the caller records them as uncovered.
func TestServicePlanReportsUnverifiableChecksWithTheirPaths(t *testing.T) {
	for _, test := range []struct {
		name       string
		when       config.WhenConfig
		wantReason string
	}{
		{"event", config.WhenConfig{Event: []string{"pull_request"}}, "not service-verifiable: when.event"},
		{"tag", config.WhenConfig{Tag: "v*"}, "not service-verifiable: when.tag"},
		{"env", config.WhenConfig{Env: map[string]string{"DEPLOY": "1"}}, "not service-verifiable: when.env.DEPLOY"},
		// An empty expected value passes the comparison against an empty context,
		// so this check would otherwise be selected on an underived fact.
		{"env empty value", config.WhenConfig{Env: map[string]string{"DEPLOY": ""}}, "not service-verifiable: when.env.DEPLOY"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := serviceConfigForTest(map[string]config.CheckConfig{
				"unit":  {},
				"gated": {When: test.when},
			})
			selected, _, skipped, err := SelectChecksForServicePlan(cfg, serviceChangedFiles(cfg, "src/app.go"), "main")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(selected, []string{"unit"}) {
				t.Fatalf("selected = %v, want only unit", selected)
			}
			if len(skipped) != 1 {
				t.Fatalf("skipped = %+v, want one entry", skipped)
			}
			if skipped[0].Name != "gated" || skipped[0].Reason != test.wantReason {
				t.Fatalf("skipped = %+v, want %q", skipped[0], test.wantReason)
			}
			if !reflect.DeepEqual(skipped[0].MatchedPaths, []string{"src/app.go"}) {
				t.Fatalf("skipped matched paths = %v, want the paths it would have covered", skipped[0].MatchedPaths)
			}
		})
	}
}

func TestServicePlanSelectsExplicitUnmatchedChecksOnlyForUnmatchedPaths(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"go": {Command: "go test ./..."},
			"ci": {Command: "make check"},
		},
		Affected: config.AffectedConfig{
			Rules:           []config.AffectedRule{{Paths: []string{"**/*.go"}, Checks: config.CommandList{"go"}}},
			UnmatchedChecks: config.CommandList{"ci"},
		},
	}
	changed := serviceChangedFiles(cfg, "src/main.go", "platform/rootfs-inputs.json")

	selected, reasons, skipped, err := SelectChecksForServicePlan(cfg, changed, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []string{"go", "ci"}) || len(skipped) != 0 {
		t.Fatalf("selected=%v skipped=%+v, want go and ci", selected, skipped)
	}
	if got := matchedPaths(reasons["go"]); !reflect.DeepEqual(got, []string{"src/main.go"}) {
		t.Fatalf("go paths = %v", got)
	}
	if got := matchedPaths(reasons["ci"]); !reflect.DeepEqual(got, []string{"platform/rootfs-inputs.json"}) {
		t.Fatalf("ci unmatched paths = %v", got)
	}
}

func TestServicePlanDoesNotUseUnmatchedCheckForUnverifiableMatchedRule(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"security": {Command: "./security-scan", When: config.WhenConfig{Event: []string{"pull_request"}}},
			"ci":       {Command: "make check"},
		},
		Affected: config.AffectedConfig{
			Rules:           []config.AffectedRule{{Paths: []string{"src/**"}, Checks: config.CommandList{"security"}}},
			UnmatchedChecks: config.CommandList{"ci"},
		},
	}

	selected, _, skipped, err := SelectChecksForServicePlan(cfg, serviceChangedFiles(cfg, "src/main.go"), "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 0 || len(skipped) != 1 || skipped[0].Name != "security" {
		t.Fatalf("selected=%v skipped=%+v, want only skipped policy-required security check", selected, skipped)
	}
}

// when.status is not caller context: it defaults to success so that ordering is
// expressed by the node condition NodesForPlans compiles, not by this comparison.
func TestServiceWhenContextFixesStatusAndLeavesUnverifiableFieldsEmpty(t *testing.T) {
	ctx := ServiceWhenContext("main")
	if ctx.Status != DefaultWhenStatus {
		t.Fatalf("status = %q, want %q", ctx.Status, DefaultWhenStatus)
	}
	if ctx.Event != "" || ctx.Tag != "" || len(ctx.Env) != 0 {
		t.Fatalf("service context carried an underived fact: %+v", ctx)
	}

	cfg := serviceConfigForTest(map[string]config.CheckConfig{
		"ordered": {When: config.WhenConfig{Status: "success"}},
	})
	selected, _, _, err := SelectChecksForServicePlan(cfg, serviceChangedFiles(cfg, "src/app.go"), "main")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []string{"ordered"}) {
		t.Fatalf("selected = %v, want the status-gated check selected", selected)
	}
}

// A caller cannot supply these values by any route: the service builds its own
// context, so a plan can never turn on a fact the service did not establish.
func TestUnverifiableWhenFieldsCoversEveryUnderivableCondition(t *testing.T) {
	if fields := UnverifiableWhenFields(config.WhenConfig{}); len(fields) != 0 {
		t.Fatalf("empty when = %v, want nothing unverifiable", fields)
	}
	derivable := config.WhenConfig{
		Status:  "success",
		Changes: config.WhenChangesConfig{Include: []string{"src/**"}},
	}
	if fields := UnverifiableWhenFields(derivable); len(fields) != 0 {
		t.Fatalf("derivable when = %v, want nothing unverifiable", fields)
	}
	all := config.WhenConfig{
		Branch: []string{"main"},
		Event:  []string{"push"},
		Tag:    "v1",
		Env:    map[string]string{"B": "2", "A": "1"},
	}
	want := []string{"when.branch", "when.event", "when.tag", "when.env.A", "when.env.B"}
	if fields := UnverifiableWhenFields(all); !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %v, want %v", fields, want)
	}
}

func TestServiceWhenPolicyClassifiesEveryWhenConfigField(t *testing.T) {
	// This is intentionally a structural allowlist. Adding a WhenConfig field
	// without deciding whether the service can derive it must fail this test.
	classified := map[string]bool{
		"Event":   false,
		"Branch":  false,
		"Tag":     false,
		"Env":     false,
		"Status":  true,
		"Changes": true,
	}
	whenType := reflect.TypeOf(config.WhenConfig{})
	if whenType.NumField() != len(classified) {
		t.Fatalf("WhenConfig has %d fields, service policy classifies %d", whenType.NumField(), len(classified))
	}
	for index := 0; index < whenType.NumField(); index++ {
		field := whenType.Field(index)
		if _, ok := classified[field.Name]; !ok {
			t.Fatalf("WhenConfig.%s has no service-derivability classification", field.Name)
		}
	}
}

// A check can select itself through `when.changes` with no affected rule in the
// file. Selection is expressed entirely as rules, so without expansion the
// service plans nothing for a change that the CLI would have checked.
func TestServicePlanSelectsChecksConfiguredThroughWhenChanges(t *testing.T) {
	cfg := config.ProjectConfig{Checks: map[string]config.CheckConfig{
		"web": {When: config.WhenConfig{Changes: config.WhenChangesConfig{Include: []string{"web/**"}}}},
	}}
	selected, reasons, _, err := SelectChecksForServicePlan(cfg, map[string][]string{"web": {"web/app.ts"}}, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []string{"web"}) {
		t.Fatalf("selected = %v, want the when.changes check", selected)
	}
	if len(reasons["web"]) != 1 || reasons["web"][0].Path != "web/app.ts" {
		t.Fatalf("reasons = %+v, want the matching path", reasons["web"])
	}

	// The exclusion side of when.changes is honoured too.
	cfg.Checks["web"] = config.CheckConfig{When: config.WhenConfig{Changes: config.WhenChangesConfig{
		Include: []string{"web/**"}, Exclude: []string{"web/docs/**"},
	}}}
	selected, _, _, err = SelectChecksForServicePlan(cfg, map[string][]string{"web": {"web/docs/readme.md"}}, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 0 {
		t.Fatalf("selected = %v, want nothing for an excluded path", selected)
	}
}

// An unverifiable check must not take a verifiable one down with it. Subsumption
// pruning only considers selected checks, so excluding before selection means an
// unverifiable parent never prunes a child that could safely run.
func TestServicePlanKeepsVerifiableChildOfUnverifiableSubsumingParent(t *testing.T) {
	cfg := config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			// An empty expected value passes the comparison against an empty context,
			// so this check would otherwise be selected and prune its child.
			"full": {When: config.WhenConfig{Env: map[string]string{"MODE": ""}}},
			"unit": {},
		},
		Affected: config.AffectedConfig{
			Rules:    []config.AffectedRule{{Paths: []string{"src/**"}, Checks: []string{"full", "unit"}}},
			Subsumes: map[string]config.CommandList{"full": {"unit"}},
		},
	}
	changed := map[string][]string{"full": {"src/app.go"}, "unit": {"src/app.go"}}

	selected, _, skipped, err := SelectChecksForServicePlan(cfg, changed, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []string{"unit"}) {
		t.Fatalf("selected = %v, want the verifiable child to survive", selected)
	}
	if len(skipped) != 1 || skipped[0].Name != "full" ||
		skipped[0].Reason != "not service-verifiable: when.env.MODE" {
		t.Fatalf("skipped = %+v, want only the unverifiable parent", skipped)
	}

	// Subsumption still applies between two verifiable checks.
	cfg.Checks["full"] = config.CheckConfig{}
	selected, _, skipped, err = SelectChecksForServicePlan(cfg, changed, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []string{"full"}) {
		t.Fatalf("selected = %v, want the parent to subsume the child", selected)
	}
	if len(skipped) != 1 || skipped[0].Reason != "subsumed" {
		t.Fatalf("skipped = %+v, want the child reported as subsumed", skipped)
	}
}
