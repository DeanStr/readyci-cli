package main

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	return options, nil
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
		ChangeMode:             fallback.ChangeMode,
		ExcludePatterns:        append([]string(nil), binding.ExcludePatterns...),
		DefaultExcludePatterns: append([]string(nil), binding.DefaultExcludePatterns...),
		IncludeUntracked:       &includeUntracked,
		UseProjectIgnore:       &useProjectIgnore,
		SourcePolicyDigest:     binding.Digest,
	}, nil
}

func verificationSourceRecipe(options syncbundlebuild.BuildOptions) *preflightapi.VerificationSourceRecipe {
	includeUntracked := true
	if options.IncludeUntracked != nil {
		includeUntracked = *options.IncludeUntracked
	}
	useProjectIgnore := true
	if options.UseProjectIgnore != nil {
		useProjectIgnore = *options.UseProjectIgnore
	}
	return &preflightapi.VerificationSourceRecipe{
		ExcludePatterns:        append([]string{}, options.ExcludePatterns...),
		DefaultExcludePatterns: append([]string{}, options.DefaultExcludePatterns...),
		IncludeUntracked:       includeUntracked,
		UseProjectIgnore:       useProjectIgnore,
		SourcePolicyDigest:     strings.TrimSpace(options.SourcePolicyDigest),
	}
}

func verificationSourceBuildOptions(recipe preflightapi.VerificationRecipe) (syncbundlebuild.BuildOptions, error) {
	if err := validateVerificationRecipe(recipe, false); err != nil {
		return syncbundlebuild.BuildOptions{}, err
	}
	includeUntracked := recipe.Source.IncludeUntracked
	useProjectIgnore := recipe.Source.UseProjectIgnore
	return syncbundlebuild.BuildOptions{
		ChangeMode:             recipe.ChangeMode,
		ExcludePatterns:        append([]string(nil), recipe.Source.ExcludePatterns...),
		DefaultExcludePatterns: append([]string(nil), recipe.Source.DefaultExcludePatterns...),
		IncludeUntracked:       &includeUntracked,
		UseProjectIgnore:       &useProjectIgnore,
		SourcePolicyDigest:     recipe.Source.SourcePolicyDigest,
	}, nil
}

func refreshVerificationSessionCurrentness(ctx context.Context, client *api.Client, session preflightapi.VerificationSession) (preflightapi.VerificationSession, error) {
	buildOptions, err := verificationSourceBuildOptions(session.Recipe)
	if err != nil {
		return session, cliConfigError("verification session does not contain a valid source recipe: "+err.Error(), err)
	}
	currentPath, current, err := syncbundlebuild.BuildToTempWithOptions(ctx, ".", buildOptions)
	if currentPath != "" {
		defer os.Remove(currentPath)
	}
	if err != nil {
		return session, err
	}
	return client.MarkVerificationSessionStale(ctx, session.SessionID, current.Manifest.SnapshotDigest)
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
		if verificationBuildOptionsKey(current) != firstKey {
			return syncbundlebuild.BuildOptions{}, fmt.Errorf(
				"affected checks %q and %q require different sync.exclude or include_untracked settings; one sealed verification session must use one exact workspace",
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
	includeUntracked := true
	if options.IncludeUntracked != nil {
		includeUntracked = *options.IncludeUntracked
	}
	if changeMode == "staged" {
		includeUntracked = false
	}
	return canonicalBuildOptions(syncbundlebuild.BuildOptions{
		ChangeMode:       changeMode,
		ExcludePatterns:  options.SyncExclude,
		IncludeUntracked: &includeUntracked,
	})
}

func canonicalBuildOptions(options syncbundlebuild.BuildOptions) syncbundlebuild.BuildOptions {
	excludes := make([]string, 0, len(options.ExcludePatterns))
	seen := map[string]bool{}
	for _, pattern := range options.ExcludePatterns {
		pattern = strings.TrimSpace(pattern)
		if pattern != "" && !seen[pattern] {
			seen[pattern] = true
			excludes = append(excludes, pattern)
		}
	}
	sort.Strings(excludes)
	options.ExcludePatterns = excludes
	return options
}

func verificationBuildOptionsKey(options syncbundlebuild.BuildOptions) string {
	includeUntracked := true
	if options.IncludeUntracked != nil {
		includeUntracked = *options.IncludeUntracked
	}
	return fmt.Sprintf("%s\x00%t\x00%s", options.ChangeMode, includeUntracked, strings.Join(options.ExcludePatterns, "\x00"))
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

func uploadVerificationSessionSource(ctx context.Context, client *api.Client, created preflightapi.CreateVerificationSessionResponse, bundlePath string, bundle syncbundlebuild.BuildResult, progress agentProgressSink, probeDirectInvariants bool) (preflightapi.VerificationSession, error) {
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
	if err := uploadVerificationBundleFile(ctx, client, prepared.UploadURL, bundlePath, bundle.Size, api.UploadBundleOptions{
		Method:                        prepared.UploadMethod,
		Headers:                       prepared.UploadHeaders,
		AllowCrossOrigin:              true,
		OmitAPIAuthentication:         true,
		TreatAlreadyUploadedAsSuccess: true,
	}); err != nil {
		return preflightapi.VerificationSession{}, err
	}
	if probeDirectInvariants {
		if err := probeVerificationSourceDirectUploadInvariants(ctx, client, prepared, bundlePath, descriptor); err != nil {
			return preflightapi.VerificationSession{}, err
		}
	}
	uploaded, err := client.CompleteVerificationSessionSourceUpload(ctx, prepared.CompleteURL, preflightapi.CompleteVerificationSessionSourceUploadRequest{
		SizeBytes:     descriptor.SizeBytes,
		ContentSHA256: descriptor.ContentSHA256,
	})
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

func watchVerificationSession(ctx context.Context, client *api.Client, sessionID string, progress agentProgressSink) (preflightapi.VerificationSession, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	previous := make(map[string]string)
	var latest preflightapi.VerificationSession
	for {
		session, err := client.VerificationSession(ctx, sessionID)
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
