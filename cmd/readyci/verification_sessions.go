package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

var errVerificationTaskMismatch = errors.New("task-id does not match predecessor verification session")

type verificationSessionOptions struct {
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
	prepared, err := prepareVerificationSession(ctx, options, intent)
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
	sourcePolicy, err := client.ProjectVerificationSourcePolicy(ctx, projectID, buildOptions.ChangeMode)
	if err != nil {
		return preflightapi.VerificationSession{}, fmt.Errorf("read registered verification source policy: %w", err)
	}
	buildOptions, err = registeredPolicyBuildOptions(buildOptions, sourcePolicy.Active)
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	bundlePath, bundle, err := syncbundlebuild.BuildToTempWithOptions(ctx, ".", buildOptions)
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	defer os.Remove(bundlePath)
	printUntrackedUploadWarning(bundle)
	emitAgentProgress(progress, agentProgressEvent{Kind: agentProgressWorkspacePackaged, SizeBytes: bundle.Size})
	req, err := verificationSessionRequest(options, intent, remote, projectID, bundle, buildOptions, matched, mode, warmPlans, plans)
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	createRequest = &req
	created, err := client.CreateVerificationSession(ctx, req)
	if err != nil {
		return preflightapi.VerificationSession{}, err
	}
	sourceUploadRequired, requirementErr := validateVerificationSourceUploadRequirements(
		created,
		options.ProbeDirectSourceUploadInvariants,
	)
	if requirementErr != nil {
		return cancelVerificationSessionRejectedAfterCreation(client, created.Session, requirementErr)
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
		)
		if err != nil {
			return created.Session, err
		}
		created.Session = uploaded
	}
	session = created.Session
	if detach {
		return session, nil
	}
	session, err = watchVerificationSession(ctx, client, created.Session.SessionID, progress)
	if err != nil {
		if session.SchemaVersion == "" {
			session = created.Session
		}
		return session, err
	}
	currentPath, current, err := syncbundlebuild.BuildToTempWithOptions(ctx, ".", buildOptions)
	if currentPath != "" {
		defer os.Remove(currentPath)
	}
	if err != nil {
		return session, err
	}
	// Currentness is an actor-scoped observation, not a one-way stale flag.
	// Always report the rebuilt digest so reverting to the verified snapshot
	// replaces an earlier stale observation with a current one.
	session, err = client.MarkVerificationSessionStale(ctx, session.SessionID, current.Manifest.SnapshotDigest)
	return session, err
}
