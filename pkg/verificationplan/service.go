package verificationplan

import (
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

// DefaultWhenStatus is the value `when.status` is compared against when nothing
// reports a prior status. It is "success" because a check that asks to run only
// after success runs by default; the real ordering semantics are expressed as a
// node condition by NodesForPlans, not by this comparison.
const DefaultWhenStatus = "success"

// The rule for service-authorized planning is a single one: a plan may only
// depend on facts the service derived. UnverifiableWhenFields is an explicit
// deny list because it must report precise customer-facing field names; the
// structural test in service_test.go fails whenever WhenConfig gains a field
// that is not deliberately classified here.
//
// Derivable from a validated bundle:
//
//   - when.changes — compiled into affected rules before selection, so it is
//     path matching over the manifest's changed files and never reaches here;
//   - when.status — not caller context at all; see DefaultWhenStatus.
//
// Not derivable, and deliberately not accepted from the caller either:
//
//   - when.branch  — the bundle manifest branch is caller-supplied metadata and
//     is not bound by the workspace content digest;
//   - when.event   — a CI event name has no server-side source;
//   - when.tag     — the manifest records branch, head, and dirty state, but no
//     tag, and the bundle carries the tree at HEAD rather than the ref graph, so
//     recording one would still leave it client-asserted;
//   - when.env     — arbitrary environment values have no server-side source.
//
// Accepting any of these as declared would let a caller plan its way around a
// required check, which is the bypass the sealed-session work closed. See ADR
// 0007.

// ServiceWhenContext is the only `when` context a service-authorized plan may be
// evaluated against. Branch, event, tag, and env are empty because the service
// has no independently trusted source for them — not because they are unset.
// The argument remains for source compatibility while ADR 0007 is implemented.
func ServiceWhenContext(_ string) WhenContext {
	return WhenContext{
		Status: DefaultWhenStatus,
	}
}

// UnverifiableWhenFields lists the `when` conditions in a check that a service
// cannot derive. A check with any of them cannot take part in a
// service-authorized plan, whichever way the comparison would have gone.
func UnverifiableWhenFields(when config.WhenConfig) []string {
	var fields []string
	if len(when.Branch) > 0 {
		fields = append(fields, "when.branch")
	}
	if len(when.Event) > 0 {
		fields = append(fields, "when.event")
	}
	if strings.TrimSpace(when.Tag) != "" {
		fields = append(fields, "when.tag")
	}
	if len(when.Env) > 0 {
		keys := make([]string, 0, len(when.Env))
		for key := range when.Env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fields = append(fields, "when.env."+key)
		}
	}
	return fields
}

// SelectChecksForServicePlan is SelectChecks restricted to what a service can
// establish for itself.
//
// A check whose selection depends on an underivable condition is not silently
// dropped and not silently run. It is reported as skipped, carrying the paths it
// would have covered in MatchedPaths, so the caller records those paths as
// uncovered. The session then reports insufficient_evidence with a harness gap,
// which is the existing vocabulary for "this change was not disproven, but the
// service cannot show it was checked" — no new terminal state, and the reason
// names the condition to change.
//
// Underivable checks are excluded before selection rather than filtered out of
// the result. A check that never enters the selected set also never acts as a
// subsuming parent; filtering afterwards would let an unverifiable check prune a
// verifiable child covering the same paths, leaving neither in the plan. It also
// gives the honest reason from the start: most underivable conditions fail their
// comparison against an empty context anyway, but the reason would then read as
// "the event did not match" rather than "the service cannot know the event", and
// the two call for different fixes.
//
// `when.changes` is expanded first. Selection is expressed entirely as affected
// rules, so a check that selects itself that way is invisible until the rule
// exists — without expansion the service would silently plan nothing for it.
func SelectChecksForServicePlan(cfg config.ProjectConfig, changedFilesByCheck map[string][]string, manifestBranch string) ([]string, map[string][]RuleMatch, []SkippedCheck, error) {
	cfg = ExpandWhenChangesRules(cfg)
	excluded := map[string]string{}
	for name, check := range cfg.Checks {
		if fields := UnverifiableWhenFields(check.When); len(fields) > 0 {
			excluded[name] = "not service-verifiable: " + strings.Join(fields, ", ")
		}
	}
	selected, reasons, skipped, err := selectChecks(cfg, changedFilesByCheck, ServiceWhenContext(manifestBranch), excluded)
	if err != nil {
		return nil, nil, nil, err
	}
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Name < skipped[j].Name })
	return selected, reasons, skipped, nil
}
