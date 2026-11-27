package verificationplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// commandSourceForRun returns the source a planner sends with a run that
// requests secrets and whose command needed a wrapper, or nil. Without it the
// control plane can only hash the generated script, which no reviewer can
// approve by naming the check's command.
func commandSourceForRun(options RunSpec) *preflightapi.RunCommandSource {
	if options.SecretsRequested == nil || !*options.SecretsRequested || !runRequiresCommandWrapper(options) {
		return nil
	}
	cacheRequirements := options.CacheRuntimeRequirements
	if cacheRequirements == nil {
		cacheRequirements = options.RuntimeRequirements
	}
	return &preflightapi.RunCommandSource{
		Command:                  options.Command,
		Steps:                    CloneRunSteps(options.Steps),
		Env:                      copyStringMap(options.Env),
		Prepare:                  append([]string(nil), options.PrepareCommands...),
		Post:                     append([]string(nil), options.PostCommands...),
		Always:                   append([]string(nil), options.AlwaysCommands...),
		CachePaths:               append([]string(nil), options.CachePaths...),
		CacheKeyFiles:            append([]string(nil), options.CacheKeyFiles...),
		CacheRuntimeRequirements: preflightapi.CloneRuntimeRequirements(cacheRequirements),
		CacheDependencyCheck:     options.CacheDependencyCheck,
		KeepFailed:               options.KeepFailed,
	}
}

func runSpecForCommandSource(source preflightapi.RunCommandSource) RunSpec {
	return RunSpec{
		Command:                  source.Command,
		Steps:                    CloneRunSteps(source.Steps),
		Env:                      copyStringMap(source.Env),
		PrepareCommands:          append([]string(nil), source.Prepare...),
		PostCommands:             append([]string(nil), source.Post...),
		AlwaysCommands:           append([]string(nil), source.Always...),
		CachePaths:               append([]string(nil), source.CachePaths...),
		CacheKeyFiles:            append([]string(nil), source.CacheKeyFiles...),
		CacheRuntimeRequirements: preflightapi.CloneRuntimeRequirements(source.CacheRuntimeRequirements),
		CacheDependencyCheck:     source.CacheDependencyCheck,
		KeepFailed:               source.KeepFailed,
	}
}

// ErrInvalidCommandSource marks a planned secret run whose command source the
// service would refuse, so a planner can report it as a configuration error.
var ErrInvalidCommandSource = errors.New("invalid command source")

// ValidateSecretCommandSource reports, before any upload, whether the command
// source a secret run of this check would carry is one the service accepts.
func ValidateSecretCommandSource(name string, run RunSpec) error {
	requested := true
	run.SecretsRequested = &requested
	source := commandSourceForRun(run)
	if source == nil {
		return nil
	}
	if _, err := normalizeCommandSource(*source); err != nil {
		return fmt.Errorf("check %q requests secrets: %w: %w", name, ErrInvalidCommandSource, err)
	}
	return nil
}

// ErrCommandSourceExceedsLimit reports that a source would render more bytes
// than RenderCommandSourceWithin allows, found before rendering it.
var ErrCommandSourceExceedsLimit = errors.New("command_source renders more than the allowed size")

// RenderCommandSource renders a source exactly as a planner renders its check
// and returns the run command. A source exists only for a command that needs a
// wrapper; any other source is an error. The renderer normalizes each field
// itself, as SecretCommandSourceHash does, so sources that hash alike render
// alike; normalizing twice would not be idempotent for every path.
func RenderCommandSource(source preflightapi.RunCommandSource) (string, error) {
	return RenderCommandSourceWithin(source, math.MaxInt)
}

// RenderCommandSourceWithin renders like RenderCommandSource but first returns
// ErrCommandSourceExceedsLimit when the source's cache overlap checks alone
// would exceed limit bytes. Everything else renders in proportion to the
// source, so a caller comparing against a submitted command spends work
// proportional to that request.
func RenderCommandSourceWithin(source preflightapi.RunCommandSource, limit int) (string, error) {
	normalized, err := normalizeCommandSource(source)
	if err != nil {
		return "", err
	}
	if cacheOverlapChecksMinBytes(len(normalized.CachePaths)) > limit {
		return "", ErrCommandSourceExceedsLimit
	}
	options := runSpecForCommandSource(source)
	if !runRequiresCommandWrapper(options) {
		return "", errors.New("command_source is only for a command that needs a wrapper")
	}
	command, err := CommandForRunSpec(options)
	if err != nil {
		return "", err
	}
	createCommand, createSteps := CommandAndSteps(options, command)
	if len(createSteps) != 0 || strings.TrimSpace(createCommand) == "" {
		return "", errors.New("command_source did not render a wrapped command")
	}
	return createCommand, nil
}

// SecretCommandSourceHash identifies a source for secret approval. It hashes
// the normalized definition, not the rendered script, so wrapper changes do
// not invalidate approvals.
func SecretCommandSourceHash(source preflightapi.RunCommandSource) (string, error) {
	normalized, err := normalizeCommandSource(source)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("readyci-secret-command-source-v1\n"), encoded...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// MaxCommandSourcePhaseCommands bounds each of a command source's prepare,
// post and always lists, as preflightapi.MaxRunSteps bounds its steps. Each
// entry renders with fixed bookkeeping, so the bounds keep rendering a source
// proportional to the request that carried it.
const MaxCommandSourcePhaseCommands = preflightapi.MaxRunSteps

// normalizeCommandSource keeps only differences that change what renders.
// Rendering and hashing both start from it, and it rejects a source before
// any rendering work that could never match a submitted command.
func normalizeCommandSource(source preflightapi.RunCommandSource) (preflightapi.RunCommandSource, error) {
	if len(source.Steps) != 0 {
		if err := preflightapi.ValidateRunSteps(source.Steps); err != nil {
			return preflightapi.RunCommandSource{}, err
		}
	}
	for phase, commands := range map[string][]string{"prepare": source.Prepare, "post": source.Post, "always": source.Always} {
		if len(commands) > MaxCommandSourcePhaseCommands {
			return preflightapi.RunCommandSource{}, fmt.Errorf("%s must contain at most %d commands", phase, MaxCommandSourcePhaseCommands)
		}
	}
	// Some fields, such as the dependency check name, are hashed into the cache
	// namespace rather than written into the script, so rendering cannot catch
	// a NUL in them; the plan's JSON storage cannot hold one either.
	if commandSourceContainsNUL(source) {
		return preflightapi.RunCommandSource{}, errors.New("command_source must not contain a NUL byte")
	}
	cachePaths, err := NormalizeCachePaths(source.CachePaths)
	if err != nil {
		return preflightapi.RunCommandSource{}, err
	}
	cacheKeyFiles, err := NormalizeCacheKeyFiles(source.CacheKeyFiles)
	if err != nil {
		return preflightapi.RunCommandSource{}, err
	}
	requirements, err := preflightapi.NormalizeRuntimeRequirements(source.CacheRuntimeRequirements)
	if err != nil {
		return preflightapi.RunCommandSource{}, err
	}
	normalized := preflightapi.RunCommandSource{
		Command:                  strings.TrimSpace(source.Command),
		Steps:                    normalizeRunSteps(source.Steps),
		Env:                      copyStringMap(source.Env),
		Prepare:                  trimCommandList(source.Prepare),
		Post:                     trimCommandList(source.Post),
		Always:                   trimCommandList(source.Always),
		CachePaths:               cachePaths,
		CacheKeyFiles:            cacheKeyFiles,
		CacheRuntimeRequirements: requirements,
		// The cache namespace hashes this name untrimmed.
		CacheDependencyCheck: source.CacheDependencyCheck,
		KeepFailed:           source.KeepFailed,
	}
	if (normalized.Command == "") == (len(normalized.Steps) == 0) {
		return preflightapi.RunCommandSource{}, errors.New("command_source needs either a command or steps")
	}
	return normalized, nil
}

// SecretApprovalCommandHash returns the command hash the control plane
// computes for a planned run, which a secret approval pairs with a workspace
// digest. It mirrors the control plane: a wrapped command hashes its source,
// a single plain step its command, and other steps the step list.
func SecretApprovalCommandHash(run preflightapi.CreateRunRequest) (string, error) {
	if run.CommandSource != nil {
		return SecretCommandSourceHash(*run.CommandSource)
	}
	steps := normalizeRunSteps(run.Steps)
	switch {
	case len(steps) == 0:
		return preflightapi.TrustedSecretCommandHash(run.Command), nil
	case strings.TrimSpace(run.Command) != "":
		return "", errors.New("a run with both a command and steps has no approvable command identity")
	case len(steps) == 1 && !steps[0].Always && len(steps[0].Env) == 0 && steps[0].TimeoutSeconds == 0:
		return preflightapi.TrustedSecretCommandHash(steps[0].Command), nil
	default:
		return preflightapi.TrustedSecretStepsHash(steps), nil
	}
}

func commandSourceContainsNUL(source preflightapi.RunCommandSource) bool {
	values := []string{source.Command, source.CacheDependencyCheck}
	values = append(values, source.Prepare...)
	values = append(values, source.Post...)
	values = append(values, source.Always...)
	values = append(values, source.CachePaths...)
	values = append(values, source.CacheKeyFiles...)
	for _, step := range source.Steps {
		values = append(values, step.Name, step.Command)
		for key, value := range step.Env {
			values = append(values, key, value)
		}
	}
	for _, env := range []map[string]string{source.Env, source.CacheRuntimeRequirements} {
		for key, value := range env {
			values = append(values, key, value)
		}
	}
	for _, value := range values {
		if strings.ContainsRune(value, 0) {
			return true
		}
	}
	return false
}
