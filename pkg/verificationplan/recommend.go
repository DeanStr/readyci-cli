package verificationplan

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

// Verification intents that change which check is recommended. The full set of
// accepted intents is validated at the API boundary; only these three alter
// selection.
const (
	IntentPrePR         = "pre-pr"
	IntentFixtureUpdate = "fixture-update"
	IntentFinalHandoff  = "final-handoff"
)

// Recommendation is the check selected for a session, why it was selected, and
// what else could have been chosen.
type Recommendation struct {
	Check        string        `json:"check"`
	Reason       string        `json:"reason"`
	Alternatives []Alternative `json:"alternatives,omitempty"`
}

// Alternative is a check that was considered but not selected.
type Alternative struct {
	Check  string `json:"check"`
	Reason string `json:"reason"`
}

// RecommendCheck picks the check to run when affected rules do not apply: an
// explicitly requested one, a fixture-producing check for fixture-update intent,
// or the first match from an intent-ordered list of conventional names.
func RecommendCheck(cfg config.ProjectConfig, explicit, goal, intent string) (Recommendation, error) {
	if len(cfg.Checks) == 0 {
		return Recommendation{}, errors.New("no checks configured")
	}
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		if _, ok := cfg.Checks[explicit]; !ok {
			return Recommendation{}, fmt.Errorf("unknown check %q (available: %s)", explicit, strings.Join(SortedCheckNames(cfg.Checks), ", "))
		}
		return Recommendation{Check: explicit, Reason: "Explicit check requested."}, nil
	}
	if intent == IntentFixtureUpdate {
		if name := fixtureUpdateCheckName(cfg); name != "" {
			return Recommendation{
				Check:        name,
				Reason:       "Selected fixture-producing check for fixture-update intent.",
				Alternatives: checkAlternatives(cfg, name),
			}, nil
		}
		return Recommendation{}, errors.New("fixture-update intent requires a configured fixture, snapshot, golden, or artifact-producing check with check artifact_paths, outputs.reviewable, or global artifacts.paths")
	}
	preferred := []string{"affected", "touched", "go", "web", "node", "python", "db", "ci", "full"}
	if intent == IntentPrePR {
		preferred = []string{"ci-fast", "ci", "full", "affected", "touched", "web-e2e", "web", "go", "platform", "db"}
	}
	if intent == IntentFinalHandoff {
		preferred = []string{"full", "ci", "ci-fast", "release", "affected", "touched", "go", "web", "platform", "db"}
	}
	if GoalRequestsBroadVerification(goal) {
		preferred = []string{"full", "ci", "affected", "touched", "go", "web", "node", "python", "db"}
	}
	for _, name := range preferred {
		if _, ok := cfg.Checks[name]; ok {
			return Recommendation{
				Check:        name,
				Reason:       recommendationReason(name, goal),
				Alternatives: checkAlternatives(cfg, name),
			}, nil
		}
	}
	names := SortedCheckNames(cfg.Checks)
	return Recommendation{
		Check:        names[0],
		Reason:       "No standard agent check alias was found; selected the first configured check.",
		Alternatives: checkAlternatives(cfg, names[0]),
	}, nil
}

func fixtureUpdateCheckName(cfg config.ProjectConfig) string {
	for _, name := range []string{"fixture-update", "fixtures", "fixture", "snapshots", "snapshot", "golden"} {
		if check, ok := cfg.Checks[name]; ok && fixtureCheckHasEffectiveArtifacts(cfg, check) {
			return name
		}
	}
	for _, name := range SortedCheckNames(cfg.Checks) {
		check := cfg.Checks[name]
		if fixtureLikeCheckName(name) && fixtureCheckHasEffectiveArtifacts(cfg, check) {
			return name
		}
	}
	for _, name := range SortedCheckNames(cfg.Checks) {
		check := cfg.Checks[name]
		if checkHasArtifacts(check) {
			return name
		}
	}
	return ""
}

func fixtureCheckHasEffectiveArtifacts(cfg config.ProjectConfig, check config.CheckConfig) bool {
	return len(cfg.Artifacts.Paths) > 0 || checkHasArtifacts(check)
}

func checkHasArtifacts(check config.CheckConfig) bool {
	return len(check.ArtifactPaths) > 0 || len(check.Outputs.Reviewable) > 0
}

func fixtureLikeCheckName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, signal := range []string{"fixture", "snapshot", "golden", "artifact"} {
		if strings.Contains(name, signal) {
			return true
		}
	}
	return false
}

// GoalRequestsBroadVerification reports whether a stated goal asks for broad
// verification rather than the narrowest affected set.
func GoalRequestsBroadVerification(goal string) bool {
	goalLower := strings.ToLower(goal)
	return strings.Contains(goalLower, "full") ||
		strings.Contains(goalLower, "final") ||
		strings.Contains(goalLower, "handoff")
}

func recommendationReason(name, goal string) string {
	if strings.TrimSpace(goal) != "" {
		return fmt.Sprintf("Selected %s as the narrowest configured check for goal %q.", name, goal)
	}
	if name == "affected" || name == "touched" {
		return fmt.Sprintf("Selected %s because it is the standard changed-file verification check.", name)
	}
	return "Selected the narrowest standard configured check available."
}

func checkAlternatives(cfg config.ProjectConfig, selected string) []Alternative {
	names := SortedCheckNames(cfg.Checks)
	out := make([]Alternative, 0, len(names))
	for _, name := range names {
		if name == selected {
			continue
		}
		out = append(out, Alternative{Check: name, Reason: "Configured Ready CI check."})
		if len(out) == 3 {
			break
		}
	}
	return out
}
