package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

var errVerificationTaskMismatch = errors.New("task-id does not match predecessor verification session")

type verificationSessionOptions struct {
	SkipWarm                          bool
	WarmChecks                        []string
	SkipConfirm                       bool
	Purpose                           string
	Command                           string
	Prepared                          *preparedVerificationSession
	Timings                           *executionTimings
	APIURL                            string
	APIToken                          string
	ProjectID                         string
	CheckName                         string
	Goal                              string
	Intent                            string
	IdempotencyKey                    string
	TaskID                            string
	PredecessorID                     string
	Changes                           string
	RequestSecrets                    bool
	RetryCount                        int
	RetryProvided                     bool
	Detach                            bool
	Progress                          agentProgressSink
	Logs                              *executionLogObserver
	ProbeDirectSourceUploadInvariants bool
}

func createAndWatchVerificationSession(options verificationSessionOptions) (session preflightapi.VerificationSession, returnedErr error) {
	apiURL := options.APIURL
	projectID, intentFlag := options.ProjectID, options.Intent
	changes := options.Changes
	requestSecrets := options.RequestSecrets
	detach, progress := options.Detach, options.Progress
	ctx, interruption, stop := interruptContext(context.Background())
	defer stop()
	var client *api.Client
	var createRequest *preflightapi.CreateVerificationSessionRequest
	defer func() {
		received := interruption.Signal()
		if received == nil {
			return
		}
		var interrupted cliInterruptedError
		session, interrupted = reconcileAndCancelInterruptedVerificationSession(apiURL, client, createRequest, session, received, returnedErr)
		returnedErr = interrupted
	}()
	intent, err := normalizeAgentIntent(intentFlag)
	if err != nil {
		return preflightapi.VerificationSession{}, cliConfigError(err.Error(), err)
	}
	if err := validateAgentSecretRequestForIntent(intent, requestSecrets); err != nil {
		return preflightapi.VerificationSession{}, cliConfigError(err.Error(), err)
	}
	changes = agentIntentDefaultChanges(intent, changes)
	options.Changes = changes
	var prepared preparedVerificationSession
	if options.Prepared != nil {
		prepared = *options.Prepared
	} else {
		prepared, err = prepareVerificationSession(ctx, options, intent)
	}
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	projectID = prepared.ProjectID
	plans, warmPlans, matched := prepared.Plans, prepared.WarmPlans, prepared.MatchedFiles
	buildOptions, mode := prepared.BuildOptions, prepared.CoverageMode
	// Planning intentionally precedes credential resolution so local configuration
	// errors remain actionable even when login is also required.
	remote, err := prepareVerificationSessionRemoteContext(ctx, options)
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	client = remote.Client
	buildOptions, err = normalizedVerificationSourceBuildOptions(buildOptions)
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	if options.Purpose != preflightapi.ExecutionPurpose {
		sourcePolicy, err := client.ProjectVerificationSourcePolicy(ctx, projectID, buildOptions.ChangeMode, options.CheckName)
		if err != nil {
			return preflightapi.VerificationSession{}, fmt.Errorf("read registered verification source policy: %w", err)
		}
		buildOptions, err = registeredPolicyBuildOptions(buildOptions, sourcePolicy.Active)
		if err != nil {
			return preflightapi.VerificationSession{}, err
		}
	}
	var sourceCache executionSourceCache
	// A caller-supplied key must produce the same immutable request after another
	// job updates the local transfer hint. Use full source for these resumable
	// requests; automatically keyed jobs can use the latest cached baseline.
	if options.Purpose == preflightapi.ExecutionPurpose && strings.TrimSpace(options.IdempotencyKey) == "" {
		sourceCache = readExecutionSourceCache(apiURL, projectID)
		buildOptions.OmitBaseline = sourceCache.SessionID != ""
	}
	packagedAt := time.Now()
	bundlePath, bundle, err := syncbundlebuild.BuildToTempWithOptions(ctx, ".", buildOptions)
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	defer os.Remove(bundlePath)
	if options.Timings != nil {
		options.Timings.PackageMS = time.Since(packagedAt).Milliseconds()
		options.Timings.UploadBytes = bundle.Size
	}
	printUntrackedUploadWarning(bundle)
	emitAgentProgress(progress, agentProgressEvent{Kind: agentProgressWorkspacePackaged, SizeBytes: bundle.Size})
	req, err := verificationSessionRequest(options, intent, remote, projectID, bundle, buildOptions, matched, mode, warmPlans, plans)
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	req.Recipe.Purpose = options.Purpose
	req.Recipe.Command = options.Command
	req.Recipe.WarmChecks = options.WarmChecks
	req.Recipe.SkipConfirm = options.SkipConfirm
	req.Recipe.SkipWarm = options.SkipWarm
	if sourceCache.BaselineKey == bundle.Manifest.BaselineKey && bundle.Manifest.BaselineRef {
		req.Recipe.Source.UploadBaselineSessionID = sourceCache.SessionID
	}
	createRequest = &req
	uploadedAt := time.Now()
	var repackagedFor time.Duration
	created, err := client.CreateVerificationSession(ctx, req)
	if options.Timings != nil {
		options.Timings.SessionCreateMS = observedDurationMS(uploadedAt)
	}
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	if created.Session.Recipe.Purpose != options.Purpose {
		return cancelVerificationSessionRejectedAfterCreation(client, created.Session, errors.New("service returned a different execution purpose"))
	}
	sourceUploadRequired, requirementErr := validateVerificationSourceUploadRequirements(
		created,
		options.ProbeDirectSourceUploadInvariants,
	)
	if requirementErr != nil {
		return cancelVerificationSessionRejectedAfterCreation(client, created.Session, requirementErr)
	}
	if acknowledged := created.SourceUploadBaselineSessionID; acknowledged != "" && (!bundle.Manifest.BaselineRef || acknowledged != req.Recipe.Source.UploadBaselineSessionID) {
		return cancelVerificationSessionRejectedAfterCreation(client, created.Session, errors.New("service returned an unexpected source upload baseline"))
	}
	if bundle.Manifest.BaselineRef && created.SourceUploadBaselineSessionID == "" {
		fullOptions := buildOptions
		fullOptions.OmitBaseline = false
		repackagedAt := time.Now()
		fullPath, full, buildErr := syncbundlebuild.BuildToTempWithOptions(ctx, ".", fullOptions)
		if buildErr != nil {
			return cancelVerificationSessionRejectedAfterCreation(client, created.Session, buildErr)
		}
		repackagedFor = time.Since(repackagedAt)
		if options.Timings != nil {
			options.Timings.PackageMS += repackagedFor.Milliseconds()
		}
		defer os.Remove(fullPath)
		if full.Manifest.SnapshotDigest != bundle.Manifest.SnapshotDigest || full.Manifest.BaselineKey != bundle.Manifest.BaselineKey {
			return cancelVerificationSessionRejectedAfterCreation(client, created.Session, errors.New("workspace changed while preparing full source; submit again"))
		}
		bundlePath, bundle = fullPath, full
	}
	if options.Timings != nil {
		options.Timings.UploadBytes = bundle.Size
	}
	// Every sealed session uploads its source exactly once, including a session
	// whose plan has no nodes: the service cannot conclude that nothing needed
	// checking until it holds bytes matching the sealed snapshot digest. Only an
	// already-terminal replay (cancelled, blocked, or previously completed) skips
	// the upload, because that session can no longer accept source.
	if sourceUploadRequired && created.Session.State == preflightapi.VerificationStateSealed {
		uploaded, err := uploadVerificationSessionSource(
			ctx, client, created, bundlePath, bundle, progress,
			options.ProbeDirectSourceUploadInvariants,
			options.Timings,
		)
		if err != nil {
			return created.Session, err
		}
		created.Session = uploaded
	}
	if options.Timings != nil {
		options.Timings.UploadMS = (time.Since(uploadedAt) - repackagedFor).Milliseconds()
	}
	session = created.Session
	if detach {
		if options.Purpose == preflightapi.ExecutionPurpose {
			rememberExecutionSource(apiURL, projectID, bundle.Manifest.BaselineKey, session)
		}
		return session, nil
	}
	waitStarted := time.Now()
	session, err = watchVerificationSession(ctx, client, created.Session.SessionID, progress, options.Logs)
	if options.Timings != nil {
		options.Timings.RemoteMS = time.Since(waitStarted).Milliseconds()
	}
	if err != nil {
		if session.SchemaVersion == "" {
			session = created.Session
		}
		return session, err
	}
	if options.Purpose == preflightapi.ExecutionPurpose {
		rememberExecutionSource(apiURL, projectID, bundle.Manifest.BaselineKey, session)
	}
	current, err := syncbundlebuild.SnapshotWithOptions(ctx, ".", buildOptions)
	if err != nil {
		return session, err
	}
	// Currentness is an actor-scoped observation, not a one-way stale flag.
	// Always report the rebuilt digest so reverting to the verified snapshot
	// replaces an earlier stale observation with a current one.
	session, err = client.MarkVerificationSessionStale(ctx, session.SessionID, current.SnapshotDigest)
	return session, err
}
