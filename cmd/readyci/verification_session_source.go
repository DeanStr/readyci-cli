package main

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

func normalizedVerificationSourceBuildOptions(options syncbundlebuild.BuildOptions) (syncbundlebuild.BuildOptions, error) {
	changeMode, err := syncbundle.NormalizeChangeMode(options.ChangeMode)
	if err != nil {
		return syncbundlebuild.BuildOptions{}, cliConfigError(err.Error(), err)
	}
	options.ChangeMode = changeMode
	options.ReviewedFixtures, err = syncbundle.NormalizeReviewedFixtures(options.ReviewedFixtures)
	if err != nil {
		return syncbundlebuild.BuildOptions{}, cliConfigError("sync.reviewed_fixtures: "+err.Error(), err)
	}
	return canonicalBuildOptions(options), nil
}

func registeredPolicyBuildOptions(
	fallback syncbundlebuild.BuildOptions,
	active *syncbundle.SourcePolicyBinding,
) (syncbundlebuild.BuildOptions, error) {
	if active == nil {
		return fallback, nil
	}
	binding, err := syncbundle.NormalizeSourcePolicyBinding(*active)
	if err != nil {
		return syncbundlebuild.BuildOptions{}, fmt.Errorf("invalid registered verification source policy: %w", err)
	}
	useProjectIgnore := false
	includeUntracked := binding.IncludeUntracked
	return syncbundlebuild.BuildOptions{
		ChangeMode:                      fallback.ChangeMode,
		ExcludePatterns:                 append([]string(nil), binding.ExcludePatterns...),
		DefaultExcludePatterns:          append([]string(nil), binding.DefaultExcludePatterns...),
		DefaultUntrackedExcludePatterns: append([]string(nil), binding.DefaultUntrackedExcludePatterns...),
		ReviewedFixtures:                append([]syncbundle.ReviewedFixture(nil), binding.ReviewedFixtures...),
		IncludeUntracked:                &includeUntracked,
		UseProjectIgnore:                &useProjectIgnore,
		SourcePolicyDigest:              binding.Digest,
	}, nil
}

func verificationSourceRecipe(options syncbundlebuild.BuildOptions) *preflightapi.VerificationSourceRecipe {
	options = canonicalBuildOptions(options)
	includeUntracked := true
	if options.IncludeUntracked != nil {
		includeUntracked = *options.IncludeUntracked
	}
	useProjectIgnore := true
	if options.UseProjectIgnore != nil {
		useProjectIgnore = *options.UseProjectIgnore
	}
	return &preflightapi.VerificationSourceRecipe{
		ExcludePatterns:                 append([]string{}, options.ExcludePatterns...),
		DefaultExcludePatterns:          append([]string{}, options.DefaultExcludePatterns...),
		DefaultUntrackedExcludePatterns: append([]string{}, options.DefaultUntrackedExcludePatterns...),
		ReviewedFixtures:                append([]syncbundle.ReviewedFixture{}, options.ReviewedFixtures...),
		IncludeUntracked:                includeUntracked,
		UseProjectIgnore:                useProjectIgnore,
		SourcePolicyDigest:              strings.TrimSpace(options.SourcePolicyDigest),
	}
}

func verificationSourceBuildOptions(recipe preflightapi.VerificationRecipe) (syncbundlebuild.BuildOptions, error) {
	if err := validateVerificationRecipe(recipe, false); err != nil {
		return syncbundlebuild.BuildOptions{}, err
	}
	includeUntracked := recipe.Source.IncludeUntracked
	useProjectIgnore := recipe.Source.UseProjectIgnore
	return syncbundlebuild.BuildOptions{
		ChangeMode:                      recipe.ChangeMode,
		ExcludePatterns:                 append([]string(nil), recipe.Source.ExcludePatterns...),
		DefaultExcludePatterns:          append([]string{}, recipe.Source.DefaultExcludePatterns...),
		DefaultUntrackedExcludePatterns: append([]string{}, recipe.Source.DefaultUntrackedExcludePatterns...),
		ReviewedFixtures:                append([]syncbundle.ReviewedFixture(nil), recipe.Source.ReviewedFixtures...),
		IncludeUntracked:                &includeUntracked,
		UseProjectIgnore:                &useProjectIgnore,
		SourcePolicyDigest:              recipe.Source.SourcePolicyDigest,
	}, nil
}

func refreshVerificationSessionCurrentness(ctx context.Context, client *api.Client, session preflightapi.VerificationSession) (preflightapi.VerificationSession, error) {
	buildOptions, err := verificationSourceBuildOptions(session.Recipe)
	if err != nil {
		return session, cliConfigError("verification session does not contain a valid source recipe: "+err.Error(), err)
	}
	current, err := syncbundlebuild.SnapshotWithOptions(ctx, ".", buildOptions)
	if err != nil {
		return session, err
	}
	return client.MarkVerificationSessionStale(ctx, session.SessionID, current.SnapshotDigest)
}

func verificationCoverage(mode string, changedFiles, proposedMatchedFiles []string) ([]string, []string) {
	if mode == "check" {
		// An explicitly selected check does not prove which changed files it
		// covers. Keep the client proposal fail-closed until the service can
		// bind configured check IDs to authoritative affected-file policy.
		proposedMatchedFiles = nil
	}
	matched := intersectSortedPaths(changedFiles, proposedMatchedFiles)
	return matched, subtractSortedPaths(changedFiles, matched)
}

func verificationTaskID(idempotencyKey, requestedTaskID, predecessorTaskID string) (string, error) {
	requestedTaskID = strings.TrimSpace(requestedTaskID)
	predecessorTaskID = strings.TrimSpace(predecessorTaskID)
	if requestedTaskID != "" && predecessorTaskID != "" && requestedTaskID != predecessorTaskID {
		return "", errVerificationTaskMismatch
	}
	if predecessorTaskID != "" {
		return predecessorTaskID, nil
	}
	if requestedTaskID != "" {
		return requestedTaskID, nil
	}
	return strings.TrimSpace(idempotencyKey), nil
}

func compatibleAffectedSessionBuildOptions(plans []affectedCheckPlan, fallback syncbundlebuild.BuildOptions) (syncbundlebuild.BuildOptions, error) {
	if len(plans) == 0 {
		return canonicalBuildOptions(fallback), nil
	}
	first := canonicalVerificationBuildOptions(plans[0].Plan.Options, fallback.ChangeMode)
	firstKey := verificationBuildOptionsKey(first)
	for _, selected := range plans[1:] {
		current := canonicalVerificationBuildOptions(selected.Plan.Options, fallback.ChangeMode)
		if current.ChangeMode != first.ChangeMode {
			return syncbundlebuild.BuildOptions{}, fmt.Errorf(
				"checks %q and %q require different changes modes (%s and %s); one session must use one exact workspace; select checks with matching modes or set --changes explicitly",
				plans[0].Plan.Name, selected.Plan.Name, first.ChangeMode, current.ChangeMode,
			)
		}
		if verificationBuildOptionsKey(current) != firstKey {
			return syncbundlebuild.BuildOptions{}, fmt.Errorf(
				"affected checks %q and %q require different sync.exclude, reviewed_fixtures, or include_untracked settings; one sealed verification session must use one exact workspace; use a shared top-level sync policy for combined checks, or run each named check separately",
				plans[0].Plan.Name, selected.Plan.Name,
			)
		}
	}
	return first, nil
}

func explicitDependencyWarmSessionBuildOptions(warmPlans, targets []checkRunPlan, fallback syncbundlebuild.BuildOptions) (syncbundlebuild.BuildOptions, error) {
	sessionPlans := make([]affectedCheckPlan, 0, len(warmPlans)+len(targets))
	for _, warmPlan := range warmPlans {
		sessionPlans = append(sessionPlans, affectedCheckPlan{Plan: warmPlan})
	}
	for _, target := range targets {
		sessionPlans = append(sessionPlans, affectedCheckPlan{Plan: target})
	}
	options, err := compatibleAffectedSessionBuildOptions(sessionPlans, fallback)
	if err != nil {
		return syncbundlebuild.BuildOptions{}, cliConfigError(err.Error(), err)
	}
	return options, nil
}

func canonicalVerificationBuildOptions(options remoteRunOptions, changeMode string) syncbundlebuild.BuildOptions {
	changeMode = defaultString(options.ChangeMode, changeMode)
	includeUntracked := true
	if options.IncludeUntracked != nil {
		includeUntracked = *options.IncludeUntracked
	}
	if changeMode == "staged" {
		includeUntracked = false
	}
	return canonicalBuildOptions(syncbundlebuild.BuildOptions{
		ChangeMode:      changeMode,
		ExcludePatterns: options.SyncExclude, ReviewedFixtures: append([]syncbundle.ReviewedFixture(nil), options.SyncReviewedFixtures...),
		IncludeUntracked: &includeUntracked,
	})
}

func canonicalBuildOptions(options syncbundlebuild.BuildOptions) syncbundlebuild.BuildOptions {
	if strings.TrimSpace(options.SourcePolicyDigest) == "" {
		if options.DefaultExcludePatterns == nil {
			options.DefaultExcludePatterns = syncbundle.DefaultSourceExcludePatterns()
		}
		if options.DefaultUntrackedExcludePatterns == nil {
			options.DefaultUntrackedExcludePatterns = syncbundle.DefaultUntrackedExcludePatterns()
		}
	}
	options.ExcludePatterns = canonicalSourcePatterns(options.ExcludePatterns)
	options.DefaultExcludePatterns = canonicalSourcePatterns(options.DefaultExcludePatterns)
	options.DefaultUntrackedExcludePatterns = canonicalSourcePatterns(options.DefaultUntrackedExcludePatterns)
	options.ReviewedFixtures = append([]syncbundle.ReviewedFixture(nil), options.ReviewedFixtures...)
	sort.Slice(options.ReviewedFixtures, func(i, j int) bool {
		left, right := options.ReviewedFixtures[i], options.ReviewedFixtures[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.SHA256 < right.SHA256
	})
	return options
}

func canonicalSourcePatterns(patterns []string) []string {
	seen := make(map[string]bool, len(patterns))
	normalized := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern != "" && !seen[pattern] {
			seen[pattern] = true
			normalized = append(normalized, pattern)
		}
	}
	sort.Strings(normalized)
	return normalized
}

func verificationBuildOptionsKey(options syncbundlebuild.BuildOptions) string {
	options = canonicalBuildOptions(options)
	includeUntracked := true
	if options.IncludeUntracked != nil {
		includeUntracked = *options.IncludeUntracked
	}
	useProjectIgnore := options.UseProjectIgnore == nil || *options.UseProjectIgnore
	// JSON keeps arbitrary legal pattern bytes from colliding with separators.
	encoded, _ := json.Marshal(struct {
		ChangeMode                      string
		IncludeUntracked                bool
		UseProjectIgnore                bool
		SourcePolicyDigest              string
		ExcludePatterns                 []string
		DefaultExcludePatterns          []string
		DefaultUntrackedExcludePatterns []string
		ReviewedFixtures                []syncbundle.ReviewedFixture
	}{options.ChangeMode, includeUntracked, useProjectIgnore, options.SourcePolicyDigest,
		options.ExcludePatterns, options.DefaultExcludePatterns, options.DefaultUntrackedExcludePatterns,
		options.ReviewedFixtures})
	return string(encoded)
}

func validateVerificationSourceUploadRequirements(created preflightapi.CreateVerificationSessionResponse, probeDirectInvariants bool) (bool, error) {
	mode := strings.TrimSpace(created.SourceUploadMode)
	prepareURL := strings.TrimSpace(created.SourceUploadPrepareURL)
	switch mode {
	case preflightapi.VerificationSourceUploadModeDirect:
		if prepareURL == "" {
			return false, cliError{
				Code:           "platform_internal",
				ExitCode:       cliExitInternal,
				Classification: preflightapi.ClassificationPlatformInternalError,
				Message:        "verification service returned an incomplete direct object-store source upload contract",
			}
		}
	case preflightapi.VerificationSourceUploadModeServerCopy:
		if prepareURL != "" {
			return false, cliError{
				Code:           "platform_internal",
				ExitCode:       cliExitInternal,
				Classification: preflightapi.ClassificationPlatformInternalError,
				Message:        "verification service returned a client upload URL for server-managed source materialization",
			}
		}
		if probeDirectInvariants {
			return false, cliError{
				Code:           "verification_source_direct_upload_probe_not_executed",
				ExitCode:       cliExitUsageOrConfig,
				Classification: preflightapi.ClassificationStateConflict,
				Message:        "direct source upload invariant probes require a client-uploaded session; use a fresh idempotency key",
			}
		}
		return false, nil
	default:
		return false, cliError{
			Code:           "platform_internal",
			ExitCode:       cliExitInternal,
			Classification: preflightapi.ClassificationPlatformInternalError,
			Message:        "verification service returned an unsupported source upload mode",
		}
	}
	if probeDirectInvariants && created.Session.State != preflightapi.VerificationStateSealed {
		return false, cliError{
			Code:           "verification_source_direct_upload_probe_not_executed",
			ExitCode:       cliExitUsageOrConfig,
			Classification: preflightapi.ClassificationStateConflict,
			Message:        "direct source upload invariant probes require a newly sealed session; use a fresh idempotency key",
		}
	}
	return true, nil
}

func uploadVerificationSessionSource(ctx context.Context, client *api.Client, created preflightapi.CreateVerificationSessionResponse, bundlePath string, bundle syncbundlebuild.BuildResult, progress agentProgressSink, probeDirectInvariants bool, timings *executionTimings) (preflightapi.VerificationSession, error) {
	preparedAt := time.Now()
	required, err := validateVerificationSourceUploadRequirements(created, probeDirectInvariants)
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	if !required {
		return preflightapi.VerificationSession{}, cliError{
			Code:           "platform_internal",
			ExitCode:       cliExitInternal,
			Classification: preflightapi.ClassificationPlatformInternalError,
			Message:        "verification source upload was invoked for server-managed source materialization",
		}
	}
	file, err := os.Open(bundlePath)
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	emitAgentProgress(progress, agentProgressEvent{Kind: agentProgressUploadStarted, RunName: "session source", RunID: created.Session.SessionID, SizeBytes: bundle.Size})
	sha256Hasher := sha256.New()
	md5Hasher := md5.New()
	if _, err := io.Copy(io.MultiWriter(sha256Hasher, md5Hasher), file); err != nil {
		file.Close()
		return preflightapi.VerificationSession{}, fmt.Errorf("hash verification source: %w", err)
	}
	if err := file.Close(); err != nil {
		return preflightapi.VerificationSession{}, err
	}
	contentSHA256 := hex.EncodeToString(sha256Hasher.Sum(nil))
	contentMD5 := base64.StdEncoding.EncodeToString(md5Hasher.Sum(nil))
	descriptor := preflightapi.PrepareVerificationSessionSourceUploadRequest{
		SizeBytes:     bundle.Size,
		ContentSHA256: contentSHA256,
		ContentMD5:    contentMD5,
	}
	prepared, err := client.PrepareVerificationSessionSourceUpload(ctx, created.SourceUploadPrepareURL, descriptor)
	if timings != nil {
		timings.SourcePrepareMS = observedDurationMS(preparedAt)
	}
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	if strings.TrimSpace(prepared.UploadURL) == "" || strings.TrimSpace(prepared.CompleteURL) == "" {
		return preflightapi.VerificationSession{}, cliError{
			Code:           "platform_internal",
			ExitCode:       cliExitInternal,
			Classification: preflightapi.ClassificationPlatformInternalError,
			Message:        "verification service returned an invalid direct source upload contract",
		}
	}
	if probeDirectInvariants {
		if err := probeVerificationSourceContentMD5(ctx, client, prepared, bundlePath, descriptor.SizeBytes); err != nil {
			return preflightapi.VerificationSession{}, err
		}
	}
	transferredAt := time.Now()
	uploadErr := uploadVerificationBundleFile(ctx, client, prepared.UploadURL, bundlePath, bundle.Size, api.UploadBundleOptions{
		Method:                        prepared.UploadMethod,
		Headers:                       prepared.UploadHeaders,
		AllowCrossOrigin:              true,
		OmitAPIAuthentication:         true,
		TreatAlreadyUploadedAsSuccess: true,
	})
	if timings != nil {
		timings.SourceTransferMS = observedDurationMS(transferredAt)
	}
	if uploadErr != nil {
		return preflightapi.VerificationSession{}, uploadErr
	}
	if probeDirectInvariants {
		if err := probeVerificationSourceDirectUploadInvariants(ctx, client, prepared, bundlePath, descriptor); err != nil {
			return preflightapi.VerificationSession{}, err
		}
	}
	validatedAt := time.Now()
	uploaded, err := client.CompleteVerificationSessionSourceUpload(ctx, prepared.CompleteURL, preflightapi.CompleteVerificationSessionSourceUploadRequest{
		SizeBytes:     descriptor.SizeBytes,
		ContentSHA256: descriptor.ContentSHA256,
	})
	if timings != nil {
		timings.SourceValidationMS = observedDurationMS(validatedAt)
	}
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	if uploaded.SessionID == "" || uploaded.SessionID != created.Session.SessionID {
		return preflightapi.VerificationSession{}, cliError{
			Code:           "platform_internal",
			ExitCode:       cliExitInternal,
			Classification: preflightapi.ClassificationPlatformInternalError,
			Message:        "verification source completion returned an invalid session receipt",
		}
	}
	if timings != nil {
		timings.SourceCompletion = uploaded.SourceCompletionTimings
	}
	emitAgentProgress(progress, agentProgressEvent{Kind: agentProgressUploadCompleted, RunName: "session source", RunID: created.Session.SessionID})
	return uploaded, nil
}

func probeVerificationSourceContentMD5(
	ctx context.Context,
	client *api.Client,
	prepared preflightapi.PrepareVerificationSessionSourceUploadResponse,
	bundlePath string,
	size int64,
) error {
	file, err := os.Open(bundlePath)
	if err != nil {
		return err
	}
	defer file.Close()
	err = client.UploadBundleWithOptions(ctx, prepared.UploadURL, &firstByteCorruptingReader{reader: file}, size, api.UploadBundleOptions{
		Method:                prepared.UploadMethod,
		Headers:               prepared.UploadHeaders,
		AllowCrossOrigin:      true,
		OmitAPIAuthentication: true,
	})
	var responseErr api.ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusBadRequest {
		return directSourceInvariantProbeError("object store did not reject a corrupted body under the signed Content-MD5 contract with HTTP 400", err)
	}
	return nil
}

func probeVerificationSourceDirectUploadInvariants(
	ctx context.Context,
	client *api.Client,
	prepared preflightapi.PrepareVerificationSessionSourceUploadResponse,
	bundlePath string,
	descriptor preflightapi.PrepareVerificationSessionSourceUploadRequest,
) error {
	err := uploadVerificationBundleFile(ctx, client, prepared.UploadURL, bundlePath, descriptor.SizeBytes, api.UploadBundleOptions{
		Method:                prepared.UploadMethod,
		Headers:               prepared.UploadHeaders,
		AllowCrossOrigin:      true,
		OmitAPIAuthentication: true,
	})
	var responseErr api.ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusPreconditionFailed {
		return directSourceInvariantProbeError("object store did not reject a duplicate conditional PUT with HTTP 412", err)
	}

	mismatchedSHA256 := strings.Repeat("0", sha256.Size*2)
	if mismatchedSHA256 == descriptor.ContentSHA256 {
		mismatchedSHA256 = strings.Repeat("f", sha256.Size*2)
	}
	_, err = client.CompleteVerificationSessionSourceUpload(ctx, prepared.CompleteURL, preflightapi.CompleteVerificationSessionSourceUploadRequest{
		SizeBytes: descriptor.SizeBytes, ContentSHA256: mismatchedSHA256,
	})
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusConflict || responseErr.Code != "source_upload_mismatch" {
		return directSourceInvariantProbeError("verification API accepted a mismatched direct-source completion descriptor", err)
	}
	return nil
}

func uploadVerificationBundleFile(ctx context.Context, client *api.Client, uploadURL, bundlePath string, size int64, options api.UploadBundleOptions) error {
	file, err := os.Open(bundlePath)
	if err != nil {
		return err
	}
	defer file.Close()
	return client.UploadBundleWithOptions(ctx, uploadURL, file, size, options)
}

type firstByteCorruptingReader struct {
	reader    io.Reader
	corrupted bool
}

func (r *firstByteCorruptingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if n > 0 && !r.corrupted {
		buffer[0] ^= 0xff
		r.corrupted = true
	}
	return n, err
}

func directSourceInvariantProbeError(message string, cause error) error {
	return cliError{
		Code:           "verification_source_direct_upload_invariant_failed",
		ExitCode:       cliExitInternal,
		Classification: preflightapi.ClassificationPlatformInternalError,
		Message:        message,
		Cause:          cause,
	}
}

func watchVerificationSession(ctx context.Context, client *api.Client, sessionID string, progress agentProgressSink, logs *executionLogObserver) (preflightapi.VerificationSession, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	previous := make(map[string]string)
	var latest preflightapi.VerificationSession
	for {
		var session preflightapi.VerificationSession
		err := retryObservationRead(ctx, func() error {
			var err error
			session, err = client.VerificationSession(ctx, sessionID)
			return err
		})
		if err != nil {
			return latest, err
		}
		latest = session
		for _, node := range session.Nodes {
			if previous[node.ID] != node.Status {
				emitAgentProgress(progress, agentProgressEvent{Kind: agentProgressNodeStatus, RunName: node.Name, RunID: node.ID, Status: node.Status})
				previous[node.ID] = node.Status
			}
		}
		if err := logs.observe(ctx, client, session); err != nil {
			return session, err
		}
		if session.State == preflightapi.VerificationStateTerminal {
			return session, nil
		}
		select {
		case <-ctx.Done():
			return session, ctx.Err()
		case <-ticker.C:
		}
	}
}

func intersectSortedPaths(changed, matched []string) []string {
	allowed := make(map[string]bool, len(changed))
	for _, path := range changed {
		allowed[path] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, path := range matched {
		if allowed[path] && !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

func subtractSortedPaths(changed, matched []string) []string {
	covered := make(map[string]bool, len(matched))
	for _, path := range matched {
		covered[path] = true
	}
	var out []string
	for _, path := range changed {
		if !covered[path] {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}
