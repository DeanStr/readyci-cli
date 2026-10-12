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
	ChangeMode       string
	ExcludePatterns  []string
	IncludeUntracked *bool
	OmitBaseline     bool
	// UseProjectIgnore defaults to true. A registered service policy sets it to
	// false so candidate-controlled .readyciignore bytes cannot narrow the
	// source view used for an authoritative plan.
	UseProjectIgnore *bool
	// DefaultExcludePatterns is set by an independently registered source
	// binding. Ordinary bundles use the builder defaults; authoritative bundles
	// use and record this exact trusted list instead of hidden client behavior.
	DefaultExcludePatterns []string
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
