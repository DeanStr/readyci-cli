package syncbundlebuild

import (
	"crypto/sha256"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type BuildResult struct {
	Root             string
	Manifest         syncbundle.Manifest
	Size             int64
	IncludeUntracked bool
	UntrackedFiles   []string
}

type BuildOptions struct {
	SourceScopes []syncbundle.SourcePolicyBinding
	// RequireRegisteredScopes is a planning precondition, never an upload hint.
	// Only an independently fetched registered policy can authorize mixed scopes.
	RequireRegisteredScopes bool
	ChangeMode              string
	ExcludePatterns         []string
	IncludeUntracked        *bool
	OmitBaseline            bool
	// UseProjectIgnore defaults to true. A registered service policy sets it to
	// false so candidate-controlled .readyciignore bytes cannot narrow the
	// source view used for an authoritative plan.
	UseProjectIgnore *bool
	// DefaultExcludePatterns is an exact source-recipe filter. Nil ordinary
	// options use builder defaults; non-nil options preserve the recorded filter.
	// Registered bundles use the independently authorized exact list, including
	// an empty list, instead of hidden client behavior.
	DefaultExcludePatterns []string
	// DefaultUntrackedExcludePatterns removes generated output only when a path
	// is not supplied by Git. Registered policies bind this exact list too.
	DefaultUntrackedExcludePatterns []string
	// ReviewedFixtures permits only exact, regular Git-tracked fixture bytes to
	// bypass the default confidentiality filter; explicit exclusions still win.
	ReviewedFixtures []syncbundle.ReviewedFixture
	// SourcePolicyDigest binds the canonical sync filter used by the builder to
	// an independently registered policy revision.
	SourcePolicyDigest string
}

type ChangedFilesResult struct {
	Root             string
	ChangeMode       string
	IncludeUntracked bool
	ExcludePatterns  []string
	ChangedFiles     []string
	UntrackedFiles   []string
}

type plannedEntry struct {
	entry  syncbundle.Entry
	source string
	data   []byte
	digest [sha256.Size]byte
}

type plannedBundle struct {
	root             string
	manifest         syncbundle.Manifest
	baseline         []plannedEntry
	workspace        []plannedEntry
	includeUntracked bool
	untrackedFiles   []string
}
