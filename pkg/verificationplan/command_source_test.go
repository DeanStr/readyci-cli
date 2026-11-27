package verificationplan

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func plannedSecretRun(t *testing.T, yaml, check string, secrets bool) preflightapi.CreateRunRequest {
	t.Helper()
	cfg, err := config.ParseProjectConfig([]byte(yaml), "commandsource")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ResolveCheck(check, cfg, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	plan.Run.SecretsRequested = &secrets
	nodes, _, err := NodesForPlans(nil, []CheckPlan{plan}, BundleIdentity{}, ClientIdentity{})
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes = %v, %v", nodes, err)
	}
	return nodes[0].Run
}

const cachedSecretCheck = "version: 1\ncache:\n  mode: cache-only\n  paths: [~/.cache/go-build]\nchecks:\n  integration:\n    command: go test ./...\n"

func TestWrappedSecretRunsCarryTheSourceTheyRenderFrom(t *testing.T) {
	run := plannedSecretRun(t, cachedSecretCheck, "integration", true)
	if run.CommandSource == nil || len(run.Steps) != 0 {
		t.Fatalf("cached secret run = %+v, want a wrapped command with its source", run)
	}
	rendered, err := RenderCommandSource(*run.CommandSource)
	if err != nil || rendered != run.Command {
		t.Fatalf("rendered source differs from the planned command (err %v)", err)
	}
	hash, err := SecretApprovalCommandHash(run)
	if err != nil {
		t.Fatal(err)
	}
	sourceHash, err := SecretCommandSourceHash(*run.CommandSource)
	if err != nil || hash != sourceHash {
		t.Fatalf("approval hash %s, source hash %s (%v)", hash, sourceHash, err)
	}
	// The bug this fixes: an approval of the check's own command text never
	// named a cached run, and neither does the generated script's hash.
	for _, other := range []string{preflightapi.TrustedSecretCommandHash("go test ./..."), preflightapi.TrustedSecretCommandHash(run.Command)} {
		if hash == other {
			t.Fatalf("approval hash %s collides with a text hash", hash)
		}
	}

	if plain := plannedSecretRun(t, cachedSecretCheck, "integration", false); plain.CommandSource != nil || plain.Command != run.Command {
		t.Fatalf("run without secrets = %+v, want the same command and no source", plain)
	}
	uncached := plannedSecretRun(t, "version: 1\ncache:\n  mode: ephemeral\nchecks:\n  integration:\n    command: go test ./...\n", "integration", true)
	if uncached.CommandSource != nil {
		t.Fatalf("unwrapped secret run carries a source: %+v", uncached.CommandSource)
	}
	if got, err := SecretApprovalCommandHash(uncached); err != nil || got != preflightapi.TrustedSecretCommandHash("go test ./...") {
		t.Fatalf("unwrapped approval hash = %s, %v; want the command's own hash", got, err)
	}
}

func TestSecretCommandSourceHashCoversWhatRenders(t *testing.T) {
	base := plannedSecretRun(t, cachedSecretCheck, "integration", true).CommandSource
	hash := func(source preflightapi.RunCommandSource) string {
		t.Helper()
		got, err := SecretCommandSourceHash(source)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	want := hash(*base)
	if base.Command != "go test ./..." || len(base.Steps) != 0 {
		t.Fatalf("source = %+v, want the check's command", base)
	}
	padded := *base
	padded.Command = "  go test ./...\n"
	if hash(padded) != want {
		t.Fatal("whitespace the renderer trims changed the approval hash")
	}
	otherPath := *base
	otherPath.CachePaths = []string{"~/.cache/go-build", "~/go/pkg/mod"}
	otherCommand := *base
	otherCommand.Command = "go test -run Other ./..."
	keepFailed := *base
	keepFailed.KeepFailed = true
	for name, source := range map[string]preflightapi.RunCommandSource{"cache paths": otherPath, "command": otherCommand, "keep_failed": keepFailed} {
		if hash(source) == want {
			t.Fatalf("changing %s kept the approval hash", name)
		}
	}
}

func TestRenderCommandSourceRejectsMalformedSources(t *testing.T) {
	cache := []string{"~/.cache/go-build"}
	for name, test := range map[string]struct {
		source preflightapi.RunCommandSource
		want   string
	}{
		"no wrapper":            {preflightapi.RunCommandSource{Command: "go test ./..."}, "needs a wrapper"},
		"command and steps":     {preflightapi.RunCommandSource{Command: "go test ./...", Steps: []preflightapi.RunStepSpec{{Name: "vet", Command: "go vet ./..."}}, CachePaths: cache}, "either a command or steps"},
		"nothing to run":        {preflightapi.RunCommandSource{CachePaths: cache}, "either a command or steps"},
		"NUL in a hashed field": {preflightapi.RunCommandSource{Command: "go test ./...", CachePaths: cache, CacheDependencyCheck: "deps\x00"}, "NUL byte"},
		"too many cache paths":  {preflightapi.RunCommandSource{Command: "go test ./...", CachePaths: manyCachePaths(MaxCachePaths + 1)}, "at most 64 distinct paths"},
		"too many steps":        {preflightapi.RunCommandSource{Steps: manySteps(preflightapi.MaxRunSteps + 1), CachePaths: cache}, "at most 32 entries"},
		"too many prepare":      {preflightapi.RunCommandSource{Command: "go test ./...", Prepare: make([]string, MaxCommandSourcePhaseCommands+1), CachePaths: cache}, "prepare must contain at most 32 commands"},
	} {
		if _, err := RenderCommandSource(test.source); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s: err = %v, want %q", name, err, test.want)
		}
	}
	// An empty step would make the renderer return the bare command while
	// hashing like the wrapped check; it is rejected instead.
	if _, err := RenderCommandSource(preflightapi.RunCommandSource{Command: "go test ./...", Steps: []preflightapi.RunStepSpec{{}}, CachePaths: cache}); err == nil {
		t.Fatal("a source with an empty step rendered")
	}
	// The text \u0000 is not a NUL byte.
	escaped := preflightapi.RunCommandSource{Command: `printf '%s' '\u0000' | jq -R .`, CachePaths: cache}
	if _, err := RenderCommandSource(escaped); err != nil {
		t.Fatalf("literal \\u0000 text rejected: %v", err)
	}
}

func manySteps(count int) []preflightapi.RunStepSpec {
	steps := make([]preflightapi.RunStepSpec, count)
	for index := range steps {
		steps[index] = preflightapi.RunStepSpec{Name: "step" + strconv.Itoa(index), Command: "true"}
	}
	return steps
}

func manyCachePaths(count int) []string {
	paths := make([]string, count)
	for index := range paths {
		paths[index] = "~/.cache/path" + strconv.Itoa(index)
	}
	return paths
}

func TestCachePathLimitCountsDistinctPathsAndAdmitsTheLargestThatFits(t *testing.T) {
	command, err := RenderCommandSource(preflightapi.RunCommandSource{Command: "true", CachePaths: manyCachePaths(MaxCachePaths)})
	if err != nil {
		t.Fatal(err)
	}
	if len(command) >= 1<<20 {
		t.Fatalf("a source at the limit renders %d bytes, more than a 1 MiB request can carry", len(command))
	}
	if _, err := SecretCommandSourceHash(preflightapi.RunCommandSource{Command: "true", CachePaths: manyCachePaths(MaxCachePaths + 1)}); err == nil {
		t.Fatal("hashing accepted a source over the cache path limit")
	}
	// Overlapping project and check lists repeat paths; only distinct ones
	// render, so only distinct ones count.
	repeated := append(manyCachePaths(MaxCachePaths), manyCachePaths(MaxCachePaths)...)
	if paths, err := NormalizeCachePaths(repeated); err != nil || len(paths) != MaxCachePaths {
		t.Fatalf("repeated paths = %d, %v; want the %d distinct ones", len(paths), err, MaxCachePaths)
	}
	cfg, err := config.ParseProjectConfig([]byte("version: 1\ncache:\n  mode: cache-only\n  paths: ["+strings.Join(manyCachePaths(MaxCachePaths+1), ", ")+"]\nchecks:\n  integration:\n    command: go test ./...\n"), "limit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveCheck("integration", cfg, ResolveOptions{}); err == nil || !strings.Contains(err.Error(), "cache.paths") {
		t.Fatalf("resolve over the limit = %v, want a cache.paths error", err)
	}
}

func TestRenderCommandSourceWithinStopsBeforeRenderingTooMuch(t *testing.T) {
	// The bound must never exceed what actually renders, or a matching
	// command could be refused.
	for count := 0; count <= MaxCachePaths; count++ {
		source := preflightapi.RunCommandSource{Command: "true", CachePaths: manyCachePaths(count)}
		if count == 0 {
			source.KeepFailed = true
		}
		rendered, err := RenderCommandSource(source)
		if err != nil {
			t.Fatal(err)
		}
		if bound := cacheOverlapChecksMinBytes(count); bound > len(rendered) {
			t.Fatalf("%d paths: bound %d exceeds rendered %d", count, bound, len(rendered))
		}
		if got, err := RenderCommandSourceWithin(source, len(rendered)); err != nil || got != rendered {
			t.Fatalf("%d paths within its own size: err %v", count, err)
		}
	}
	// A compact source near the cap would render about 1 MiB; against a short
	// submitted command it is refused before rendering.
	if _, err := RenderCommandSourceWithin(preflightapi.RunCommandSource{Command: "true", CachePaths: manyCachePaths(MaxCachePaths)}, 4096); !errors.Is(err, ErrCommandSourceExceedsLimit) {
		t.Fatalf("oversized source = %v, want ErrCommandSourceExceedsLimit", err)
	}
}

func TestCachePathNormalizationIsIdempotent(t *testing.T) {
	// The service normalizes a command source the planner already normalized;
	// a value that changes meaning on the second pass is refused up front.
	for _, value := range []string{"./~/tool.lock", "~", "a/../~/x"} {
		if _, err := NormalizeCacheKeyFiles([]string{value}); err == nil {
			t.Fatalf("key file %q accepted", value)
		}
		if _, err := NormalizeCachePaths([]string{value}); err == nil && value != "~" {
			t.Fatalf("cache path %q accepted", value)
		}
	}
	for _, values := range [][]string{{"~/.cache/go-build", "node_modules", "./vendor/../deps", "/opt/tool"}} {
		once, err := NormalizeCachePaths(values)
		if err != nil {
			t.Fatal(err)
		}
		twice, err := NormalizeCachePaths(once)
		if err != nil || strings.Join(once, ",") != strings.Join(twice, ",") {
			t.Fatalf("cache paths %v then %v (%v)", once, twice, err)
		}
	}
	for _, values := range [][]string{{"go.sum", "./cli/go.sum", "a/../package-lock.json"}} {
		once, err := NormalizeCacheKeyFiles(values)
		if err != nil {
			t.Fatal(err)
		}
		twice, err := NormalizeCacheKeyFiles(once)
		if err != nil || strings.Join(once, ",") != strings.Join(twice, ",") {
			t.Fatalf("key files %v then %v (%v)", once, twice, err)
		}
	}
}

func TestPlanningReportsSecretSourceLimits(t *testing.T) {
	prepare := make([]string, MaxCommandSourcePhaseCommands+1)
	for index := range prepare {
		prepare[index] = "\"echo " + strconv.Itoa(index) + "\""
	}
	yaml := "version: 1\ncache:\n  mode: cache-only\n  paths: [~/.cache/go-build]\nchecks:\n  integration:\n    command: go test ./...\n    prepare: [" + strings.Join(prepare, ", ") + "]\n"
	cfg, err := config.ParseProjectConfig([]byte(yaml), "limits")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ResolveCheck("integration", cfg, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secrets := true
	plan.Run.SecretsRequested = &secrets
	if _, _, err := NodesForPlans(nil, []CheckPlan{plan}, BundleIdentity{}, ClientIdentity{}); err == nil || !strings.Contains(err.Error(), "prepare must contain at most") {
		t.Fatalf("planning a secret check over the phase limit = %v, want the limit reported", err)
	}
	secrets = false
	if _, _, err := NodesForPlans(nil, []CheckPlan{plan}, BundleIdentity{}, ClientIdentity{}); err != nil {
		t.Fatalf("the same check without secrets: %v", err)
	}
}

func TestServiceRenderingMatchesPlanningForPathsThatNormalizeOnce(t *testing.T) {
	// "./ leading" cleans to " leading"; normalizing a second time would trim
	// it to another path. The service renders the source as planned.
	yaml := "version: 1\ncache:\n  mode: cache-only\n  paths: [\"./ leading\", \"~/.cache/go-build\"]\n  key_files: [\"./ leading.lock\"]\nchecks:\n  integration:\n    command: go test ./...\n"
	run := plannedSecretRun(t, yaml, "integration", true)
	if run.CommandSource == nil {
		t.Fatal("cached secret run has no source")
	}
	rendered, err := RenderCommandSource(*run.CommandSource)
	if err != nil || rendered != run.Command {
		t.Fatalf("service rendering differs from planning (err %v)", err)
	}
	within, err := RenderCommandSourceWithin(*run.CommandSource, len(run.Command))
	if err != nil || within != run.Command {
		t.Fatalf("bounded service rendering differs from planning (err %v)", err)
	}
}

func TestResolveCheckRejectsCacheKeyFilesTheRendererRefuses(t *testing.T) {
	cfg, err := config.ParseProjectConfig([]byte("version: 1\ncache:\n  mode: cache-only\n  paths: [~/.cache/go-build]\n  key_files: [\"./~/tool.lock\"]\nchecks:\n  integration:\n    command: go test ./...\n"), "keyfiles")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveCheck("integration", cfg, ResolveOptions{}); err == nil || !strings.Contains(err.Error(), "cache.key_files") {
		t.Fatalf("resolve with an ambiguous key file = %v, want a cache.key_files error", err)
	}
}

func cacheConsumersPolicy(prefix string) string {
	quoted := func(paths []string) string {
		return "[\"" + strings.Join(paths, "\", \"") + "\"]"
	}
	first, second := make([]string, 40), make([]string, 40)
	for index := range first {
		first[index] = "~/.cache/first" + strconv.Itoa(index)
		second[index] = "~/.cache/second" + strconv.Itoa(index)
	}
	return prefix + "cache:\n  mode: cache-only\nchecks:\n  dependencies:\n    command: npm ci\n  first:\n    command: npm test\n    cache:\n      paths: " + quoted(first) + "\n  second:\n    command: npm run lint\n    cache:\n      paths: " + quoted(second) + "\n"
}

func TestDependencyWarmRejectsACombinedCachePathSetOverTheCap(t *testing.T) {
	cfg, err := config.ParseProjectConfig([]byte(cacheConsumersPolicy("version: 1\n")), "union")
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(name string) CheckPlan {
		t.Helper()
		plan, err := ResolveCheck(name, cfg, ResolveOptions{})
		if err != nil {
			t.Fatalf("resolve %s: %v", name, err)
		}
		return plan
	}
	_, err = DependencyWarmPlansForTargets(resolve("dependencies"), []CheckPlan{resolve("first"), resolve("second")})
	if err == nil || !strings.Contains(err.Error(), "would link 80 distinct cache paths") {
		t.Fatalf("warm plan for 80 combined paths = %v, want the cap reported", err)
	}
	if _, err := DependencyWarmPlansForTargets(resolve("dependencies"), []CheckPlan{resolve("first")}); err != nil {
		t.Fatalf("warm plan for one consumer: %v", err)
	}
}
