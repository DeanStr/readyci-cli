package main

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// Runtime declarations are advisory. Setup seals a declaration only when it is
// a supported numeric requirement a worker can satisfy; anything else stays a
// visible unresolved item (see setupDeclarationReview) and the runner image's
// installed toolchain is used.

// setupTranslatableRuntimeRequirement returns a sealed requirement or the reason
// a declaration stays advisory. Moving or named selectors and unsupported
// syntax are never resolved locally. A package-manager integrity pin is kept only
// when an approved provider archive matches it: installed image inventory can
// never prove such a pin, so any other pin would make the check unschedulable.
func setupTranslatableRuntimeRequirement(runtime, value string, normalize func(string, string) (string, error)) (string, string) {
	value = strings.TrimSpace(value)
	if runtime == "pnpm" && strings.Contains(value, "+") {
		if _, _, ok := preflightapi.PackageManagerIntegrityRequirement(value); !ok {
			return "", "package-manager integrity metadata must be an exact version with a complete sha512 digest"
		}
		if !preflightapi.ApprovedPnpmIntegrityArchive(value) {
			return "", "no approved Ready CI pnpm archive matches this integrity pin, and installed image inventory cannot prove one"
		}
		return value, ""
	}
	normalized, err := normalize(runtime, value)
	if err == nil {
		return normalized, ""
	}
	if setupMovingRuntimeSelector(value) {
		return "", "moving, named or variant selectors are not resolved by setup"
	}
	return "", "unsupported version syntax (" + err.Error() + ")"
}

// setupNoteValue quotes a declared value for a review note, abbreviating long
// integrity digests.
func setupNoteValue(value string) string {
	if len(value) > 48 {
		value = value[:40] + "..."
	}
	return strconv.Quote(value)
}

func setupMovingRuntimeSelector(value string) bool {
	for _, ch := range strings.TrimPrefix(strings.TrimSpace(value), "v") {
		if unicode.IsLetter(ch) && ch != 'x' && ch != 'X' {
			return true
		}
	}
	return false
}

// setupNumericRuntimeRequirement normalizes a declaration already expressed in
// Ready CI's numeric grammar.
func setupNumericRuntimeRequirement(runtime, value string) (string, error) {
	normalized, err := preflightapi.NormalizeRuntimeRequirements(map[string]string{runtime: value})
	if err != nil {
		return "", err
	}
	return normalized[runtime], nil
}
