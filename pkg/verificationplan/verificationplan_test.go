package verificationplan

import (
	"os"
	"reflect"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

func branchGatedConfig() config.ProjectConfig {
	return config.ProjectConfig{
		Checks: map[string]config.CheckConfig{
			"unit":     {},
			"security": {When: config.WhenConfig{Branch: []string{"main"}}},
		},
		Affected: config.AffectedConfig{Rules: []config.AffectedRule{
			{Paths: []string{"src/**"}, Checks: []string{"unit", "security"}},
		}},
	}
}

// Planning must depend only on the context it is given. The service cannot read
// the caller's environment and must not inherit its own, so a matcher that
// reached for os.Getenv would silently plan against the API host's environment.
func TestSelectChecksIgnoresProcessEnvironment(t *testing.T) {
	t.Setenv("READYCI_BRANCH", "main")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("READYCI_EVENT_NAME", "push")

	cfg := branchGatedConfig()
	changed := map[string][]string{"unit": {"src/app.go"}, "security": {"src/app.go"}}

	selected, _, skipped, err := SelectChecks(cfg, changed, WhenContext{Branch: "feature/x"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []string{"unit"}) {
		t.Fatalf("selected = %v, want only unit; process environment leaked into planning", selected)
	}
	if len(skipped) != 1 || skipped[0].Name != "security" || skipped[0].Reason != "when.ref" {
		t.Fatalf("skipped = %+v, want security skipped by when.ref", skipped)
	}

	selected, _, _, err = SelectChecks(cfg, changed, WhenContext{Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, []string{"unit", "security"}) {
		t.Fatalf("selected = %v, want both checks on main", selected)
	}
}

// The service derives one changed-file list from the validated bundle manifest,
// while the CLI scans once per distinct check sync config. Those agree only
// while one session means one workspace — see compatibleAffectedSessionBuildOptions.
// If that constraint is ever relaxed, this is where it should surface.
func TestSelectChecksAgreesForSharedAndPerCheckChangedFiles(t *testing.T) {
	cfg := branchGatedConfig()
	ctx := WhenContext{Branch: "main"}
	shared := []string{"src/app.go", "docs/readme.md"}

	fromShared, sharedReasons, _, err := SelectChecks(cfg, map[string][]string{
		"unit": shared, "security": shared,
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	fromPerCheck, perCheckReasons, _, err := SelectChecks(cfg, map[string][]string{
		"unit": append([]string(nil), shared...), "security": append([]string(nil), shared...),
	}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromShared, fromPerCheck) || !reflect.DeepEqual(sharedReasons, perCheckReasons) {
		t.Fatalf("shared = %v/%v, per-check = %v/%v", fromShared, sharedReasons, fromPerCheck, perCheckReasons)
	}
}

func TestExpandMatrixIsPure(t *testing.T) {
	t.Setenv("READYCI_MATRIX", "should-be-ignored")
	variants, err := ExpandMatrix(config.MatrixConfig{Axes: map[string][]string{
		"NODE": {"20", "22"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 2 || variants[0].Env["NODE"] != "20" || variants[1].Env["NODE"] != "22" {
		t.Fatalf("variants = %+v", variants)
	}
}

func TestPackageDoesNotReadTheEnvironment(t *testing.T) {
	// Guard rail for the invariant the package documents. os is imported by this
	// test only; any production file reaching for it should fail review here.
	if _, ok := os.LookupEnv("READYCI_BRANCH"); ok {
		t.Skip("ambient READYCI_BRANCH would make this check meaningless")
	}
	cfg := branchGatedConfig()
	changed := map[string][]string{"unit": {"src/app.go"}, "security": {"src/app.go"}}
	first, _, _, err := SelectChecks(cfg, changed, WhenContext{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("READYCI_BRANCH", "main")
	second, _, _, err := SelectChecks(cfg, changed, WhenContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("planning changed with the environment: %v then %v", first, second)
	}
}

// Matrix variant identity is easy to get subtly wrong: the logical child id and
// the command alias carry the variant label, the display name does not, and the
// run specification itself is unchanged apart from the variant environment.
func TestNodesForPlansDerivesMatrixVariantIdentity(t *testing.T) {
	nodes, groups, err := NodesForPlans(nil, []CheckPlan{{
		Name:   "test",
		Run:    RunSpec{Command: "go test ./...", CommandAlias: "test"},
		Matrix: config.MatrixConfig{Axes: map[string][]string{"GO": {"1.24", "1.25"}}},
	}}, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || len(groups) != 1 {
		t.Fatalf("nodes = %d, groups = %d", len(nodes), len(groups))
	}
	for index, want := range []struct{ id, name, alias, env string }{
		{"test:GO=1.24", "test", "test [GO=1.24]", "1.24"},
		{"test:GO=1.25", "test", "test [GO=1.25]", "1.25"},
	} {
		node := nodes[index]
		if node.ID != want.id || node.Name != want.name || node.Run.CommandAlias != want.alias {
			t.Fatalf("node %d = id %q name %q alias %q, want %q/%q/%q",
				index, node.ID, node.Name, node.Run.CommandAlias, want.id, want.name, want.alias)
		}
		if node.MatrixGroup != groups[0].ID {
			t.Fatalf("node %d matrix group = %q, want %q", index, node.MatrixGroup, groups[0].ID)
		}
	}
}

// A check with no command alias falls back to a stable literal rather than
// producing an empty node id.
func TestNodesForPlansFallsBackToCheckNameForIdentity(t *testing.T) {
	nodes, _, err := NodesForPlans(nil, []CheckPlan{{
		Name: "lint",
		Run:  RunSpec{Command: "true"},
	}}, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != "check" || nodes[0].Name != "lint" {
		t.Fatalf("node = %+v, want id \"check\" and name \"lint\"", nodes[0])
	}
}

// The divergence this fixes: sequential execution tracks failure cumulatively,
// so once anything fails every later `when.status: success` check is skipped
// even if an unconditional check succeeded in between. A graph carrying only the
// immediate predecessor would have run the last check.
func TestNodesForPlansAccumulatesSuccessDependenciesForStatusGatedChecks(t *testing.T) {
	plan := func(name, status string) CheckPlan {
		return CheckPlan{
			Name: name,
			Run:  RunSpec{Command: "true", CommandAlias: name},
			When: config.WhenConfig{Status: status},
		}
	}
	nodes, _, err := NodesForPlans(nil, []CheckPlan{
		plan("first", ""),
		plan("second", ""),
		plan("third", "success"),
	}, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(nodes))
	}
	third := nodes[2]
	if third.Condition != "on_success" {
		t.Fatalf("third condition = %q, want on_success", third.Condition)
	}
	if !reflect.DeepEqual(third.DependsOn, []string{"second", "first"}) {
		t.Fatalf("third depends on %v, want every earlier node", third.DependsOn)
	}
	// Unconditional checks keep depending only on the plan before them, so
	// independent work still runs after a failure.
	if nodes[0].Condition != "always" || len(nodes[0].DependsOn) != 0 {
		t.Fatalf("first = %+v, want an unconditional root", nodes[0])
	}
	if nodes[1].Condition != "always" || !reflect.DeepEqual(nodes[1].DependsOn, []string{"first"}) {
		t.Fatalf("second = %+v, want unconditional after first only", nodes[1])
	}
}

// Matrix siblings run concurrently, so accumulating earlier nodes must not make
// a status-gated variant wait on its own siblings.
func TestNodesForPlansDoesNotMakeStatusGatedMatrixSiblingsDependOnEachOther(t *testing.T) {
	nodes, _, err := NodesForPlans(nil, []CheckPlan{
		{Name: "setup", Run: RunSpec{Command: "true", CommandAlias: "setup"}},
		{
			Name:   "test",
			Run:    RunSpec{Command: "true", CommandAlias: "test"},
			When:   config.WhenConfig{Status: "success"},
			Matrix: config.MatrixConfig{Axes: map[string][]string{"GO": {"1.24", "1.25"}}},
		},
	}, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(nodes))
	}
	for _, node := range nodes[1:] {
		if !reflect.DeepEqual(node.DependsOn, []string{"setup"}) {
			t.Fatalf("%s depends on %v, want only the earlier plan", node.ID, node.DependsOn)
		}
	}
}

// Planner-inserted work carries an identity that says what it is for. Deriving
// the identity from the command alias instead would rename it, and would collide
// with the user's own check of the same name.
func TestNodesForPlansKeepsPlannerAssignedIdentity(t *testing.T) {
	warm := CheckPlan{
		Name:             "rust-deps",
		Run:              RunSpec{Command: "cargo fetch", CommandAlias: "rust-deps"},
		LogicalChildID:   "warm:rust-deps",
		LogicalChildName: "warm rust-deps",
	}
	verify := CheckPlan{Name: "rust-deps", Run: RunSpec{Command: "cargo test", CommandAlias: "rust-deps"}}

	nodes, _, err := NodesForPlans([]CheckPlan{warm}, []CheckPlan{verify}, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	if nodes[0].ID != "warm:rust-deps" || nodes[0].Name != "warm rust-deps" {
		t.Fatalf("warm node = id %q name %q, want the assigned identity", nodes[0].ID, nodes[0].Name)
	}
	// The user's own check keeps its own name rather than being pushed to a
	// deduplicated suffix by the inserted node.
	if nodes[1].ID != "rust-deps" || nodes[1].Name != "rust-deps" {
		t.Fatalf("verify node = id %q name %q, want its own name", nodes[1].ID, nodes[1].Name)
	}
	if !reflect.DeepEqual(nodes[1].SuccessDependsOn, []string{"warm:rust-deps"}) {
		t.Fatalf("verify success dependencies = %v, want the warm node id", nodes[1].SuccessDependsOn)
	}
}

// A matrix variant still qualifies an assigned identity, so variants of an
// inserted node stay distinguishable.
func TestNodesForPlansQualifiesAssignedIdentityPerVariant(t *testing.T) {
	nodes, _, err := NodesForPlans(nil, []CheckPlan{{
		Name:             "deps",
		Run:              RunSpec{Command: "true", CommandAlias: "deps"},
		LogicalChildID:   "warm:deps",
		LogicalChildName: "warm deps",
		Matrix:           config.MatrixConfig{Axes: map[string][]string{"GO": {"1.24", "1.25"}}},
	}}, BundleIdentity{}, ClientIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"warm:deps:GO=1.24", "warm:deps:GO=1.25"} {
		if nodes[index].ID != want {
			t.Fatalf("node %d id = %q, want %q", index, nodes[index].ID, want)
		}
		if nodes[index].Name != "warm deps" {
			t.Fatalf("node %d name = %q, want the assigned name unqualified", index, nodes[index].Name)
		}
	}
}
