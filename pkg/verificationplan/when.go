package verificationplan

import (
	"regexp"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

// WhenContext is the execution context a check's `when` conditions are evaluated
// against.
//
// It is a parameter rather than something this package derives, because the two
// callers obtain it from sources with very different trust:
//
//   - the CLI reads it from the local process environment (READYCI_*, GITHUB_*),
//     which is exactly right for a developer or a CI job describing itself;
//   - the service must not. A caller that supplies its own context could claim
//     Branch "feature" to skip a rule gated on "main". Only Branch and Tag are
//     recoverable from a validated bundle manifest; Event, Status, and Env are
//     not, so service-side planning has to decide what it will honour rather
//     than trust whatever arrives.
//
// See ADR 0007.
type WhenContext struct {
	Event  string
	Branch string
	Tag    string
	Status string
	Env    map[string]string
}

// WhenMatches reports whether a check's `when` conditions hold in ctx. The
// second result names the first condition that failed, for skip reporting.
func WhenMatches(when config.WhenConfig, ctx WhenContext) (bool, string) {
	if len(when.Event) > 0 && !stringListContains(when.Event, ctx.Event) {
		return false, "when.event"
	}
	if !whenRefMatches(when, ctx) {
		return false, "when.ref"
	}
	for key, value := range when.Env {
		if ctx.Env[key] != value {
			return false, "when.env." + key
		}
	}
	if status := strings.TrimSpace(when.Status); status != "" && status != strings.TrimSpace(ctx.Status) {
		return false, "when.status"
	}
	return true, ""
}

func whenRefMatches(when config.WhenConfig, ctx WhenContext) bool {
	hasBranchCondition := len(when.Branch) > 0
	hasTagCondition := strings.TrimSpace(when.Tag) != ""
	if !hasBranchCondition && !hasTagCondition {
		return true
	}
	if ctx.Branch != "" && hasBranchCondition {
		for _, pattern := range when.Branch {
			if GlobMatches(pattern, ctx.Branch) {
				return true
			}
		}
	}
	if ctx.Tag != "" && hasTagCondition && GlobMatches(when.Tag, ctx.Tag) {
		return true
	}
	return false
}

func stringListContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// GlobMatches matches a ref glob (`*` and `?`) against a branch or tag name.
func GlobMatches(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	value = strings.TrimSpace(value)
	if pattern == "" || value == "" {
		return false
	}
	if !strings.ContainsAny(pattern, "*?") {
		return pattern == value
	}
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	return regexp.MustCompile(b.String()).MatchString(value)
}
