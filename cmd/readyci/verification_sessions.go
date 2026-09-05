package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/syncbundlebuild"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

var errVerificationTaskMismatch = errors.New("task-id does not match predecessor verification session")

func verificationCommand(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	projectID := fs.String("project", envDefault("READYCI_PROJECT_ID", ""), "project id")
	checkName := fs.String("check", "", "configured check to run; defaults to affected verification or an inferred check")
	goal := fs.String("goal", "", "short verification objective")
	intentFlag := fs.String("intent", "", "verification intent")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for resumable verification")
	taskID := fs.String("task-id", "", "stable task identifier shared by successor sessions")
	predecessorID := fs.String("predecessor", "", "prior verification session for this task")
	changes := fs.String("changes", "", "changed-file mode: working, staged, or all")
	requestSecrets := fs.Bool("request-secrets", false, "request authorized project secrets")
	retryCount := fs.Int("retry", -1, "maximum retries per node")
	jsonOutput := fs.Bool("json", false, "print one machine-readable JSON document")
	progressMode := fs.String("progress", envDefault("READYCI_PROGRESS", "auto"), "progress output: auto, status, or quiet")
	detach := fs.Bool("detach", false, "create and upload the sealed session without waiting for completion")
	probeDirectSourceUploadInvariants := fs.Bool("probe-direct-source-upload-invariants", false, "verify conditional PUT, Content-MD5, and descriptor enforcement on the advertised direct source upload")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 0 {
		return cliConfigError("usage: readyci verify [--json] [--detach] [--check NAME] [--intent INTENT] [--goal TEXT] [--task-id ID] [--idempotency-key KEY] [--predecessor SESSION_ID]", nil)
	}
	if flagProvided(fs, "retry") && *retryCount < 0 {
		return cliConfigError("retry count must be non-negative", nil)
	}
	progress, err := newAgentProgressReporter(*progressMode, *jsonOutput, os.Stderr, stderrIsInteractive())
	if err != nil {
		return cliConfigError(err.Error(), err)
	}
	if progress.mode == agentProgressLogs {
		return cliConfigError("progress mode logs is not available for verification sessions; use readyci runs logs for a node run", nil)
	}
	session, runErr := createAndWatchVerificationSession(verificationSessionOptions{
		APIURL: *apiURL, APIToken: *apiToken, ProjectID: *projectID, CheckName: *checkName, Goal: *goal, Intent: *intentFlag,
		IdempotencyKey: *idempotencyKey, TaskID: *taskID, PredecessorID: *predecessorID, Changes: *changes,
		RequestSecrets: *requestSecrets, RetryCount: *retryCount, RetryProvided: flagProvided(fs, "retry"), Detach: *detach, Progress: progress,
		ProbeDirectSourceUploadInvariants: *probeDirectSourceUploadInvariants,
	})
	runErr = newCLISecretRedactor(args).RedactError(runErr)
	var interrupted cliInterruptedError
	if errors.As(runErr, &interrupted) {
		if session.SchemaVersion == "" {
			return runErr
		}
		if *jsonOutput {
			if err := writeJSONOutput(session); err != nil {
				return err
			}
		} else {
			printVerificationSession(session)
			fmt.Fprintln(os.Stderr, "readyci:", newCLISecretRedactor(args).Redact(interrupted.Error()))
		}
		return exitCodeError{code: interruptionExitCode(interrupted.Signal)}
	}
	commandErrorExitCode := 0
	if session.SchemaVersion != "" && runErr != nil {
		commandErrorExitCode = applyVerificationCommandError(&session, runErr)
	}
	if session.SchemaVersion != "" {
		if *jsonOutput {
			if err := writeJSONOutput(session); err != nil {
				return err
			}
		} else {
			printVerificationSession(session)
		}
	}
	if runErr != nil {
		if session.SchemaVersion == "" {
			return runErr
		}
		if !*jsonOutput {
			fmt.Fprintln(os.Stderr, "readyci:", runErr)
		}
		return exitCodeError{code: commandErrorExitCode}
	}
	if *detach {
		return nil
	}
	return verificationVerdictExit(session)
}

func applyVerificationCommandError(session *preflightapi.VerificationSession, err error) int {
	classification := classifyVerificationCommandError(err)
	normalized := normalizeCLIError(err)
	if session != nil {
		session.ClientError = &preflightapi.VerificationClientError{
			Code:              normalized.Code,
			Classification:    classification,
			Retryable:         normalized.Retryable,
			RetryAfterSeconds: normalized.RetryAfterSeconds,
			Message:           normalized.Message,
		}
	}
	return normalized.ExitCode
}

func classifyVerificationCommandError(err error) string {
	if errors.Is(err, errVerificationTaskMismatch) {
		return preflightapi.ClassificationConfigInvalid
	}
	return classifyCommandError(err)
}

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

type verificationSessionRemoteContext struct {
	Client         *api.Client
	IdempotencyKey string
	TaskID         string
	PredecessorID  string
}

func prepareVerificationSessionRemoteContext(
	ctx context.Context,
	options verificationSessionOptions,
) (verificationSessionRemoteContext, error) {
	token, err := effectiveAPIToken(options.APIURL, options.APIToken)
	if err != nil {
		return verificationSessionRemoteContext{}, err
	}
	if strings.TrimSpace(token) == "" {
		return verificationSessionRemoteContext{}, cliAuthMissingError("No Ready CI credential is available.", nil)
	}
	baseKey, err := normalizeIdempotencyKey(options.IdempotencyKey)
	if err != nil {
		return verificationSessionRemoteContext{}, cliConfigError(err.Error(), err)
	}
	if baseKey == "" {
		baseKey, err = newIdempotencyKey()
		if err != nil {
			return verificationSessionRemoteContext{}, err
		}
	}
	remote := verificationSessionRemoteContext{
		Client:         api.NewClient(options.APIURL, api.WithToken(token)),
		IdempotencyKey: baseKey,
		TaskID:         strings.TrimSpace(options.TaskID),
		PredecessorID:  strings.TrimSpace(options.PredecessorID),
	}
	predecessorTaskID := ""
	if remote.PredecessorID != "" {
		predecessor, predecessorErr := remote.Client.VerificationSession(ctx, remote.PredecessorID)
		if predecessorErr != nil {
			return verificationSessionRemoteContext{}, fmt.Errorf("read predecessor verification session: %w", predecessorErr)
		}
		predecessorTaskID = defaultString(strings.TrimSpace(predecessor.TaskID), strings.TrimSpace(predecessor.IdempotencyKey))
	}
	remote.TaskID, err = verificationTaskID(baseKey, remote.TaskID, predecessorTaskID)
	if err != nil {
		return verificationSessionRemoteContext{}, cliConfigError(err.Error(), err)
	}
	return remote, nil
}

func verificationSessionRequest(
	options verificationSessionOptions,
	intent string,
	remote verificationSessionRemoteContext,
	projectID string,
	bundle syncbundlebuild.BuildResult,
	buildOptions syncbundlebuild.BuildOptions,
	matched []string,
	coverageMode string,
	warmPlans, plans []checkRunPlan,
) (preflightapi.CreateVerificationSessionRequest, error) {
	matched, uncovered := verificationCoverage(coverageMode, bundle.Manifest.ChangedFiles, matched)
	nodes, matrixGroups, err := verificationNodesForPlans(warmPlans, plans, bundle)
	if err != nil {
		return preflightapi.CreateVerificationSessionRequest{}, err
	}
	recipeRetryCount := options.RetryCount
	if !options.RetryProvided {
		recipeRetryCount = 0
	}
	return preflightapi.CreateVerificationSessionRequest{
		SchemaVersion:  preflightapi.VerificationSchemaVersion,
		ProjectID:      projectID,
		IdempotencyKey: remote.IdempotencyKey,
		TaskID:         remote.TaskID,
		PredecessorID:  remote.PredecessorID,
		Intent:         intent,
		Goal:           strings.TrimSpace(options.Goal),
		Recipe: preflightapi.VerificationRecipe{
			SchemaVersion:  preflightapi.VerificationRecipeSchemaVersion,
			Rerunnable:     true,
			Check:          strings.TrimSpace(options.CheckName),
			ChangeMode:     bundle.Manifest.ChangeMode,
			RequestSecrets: options.RequestSecrets,
			RetryCount:     recipeRetryCount,
			RetryProvided:  options.RetryProvided,
			Source:         verificationSourceRecipe(buildOptions),
		},
		Snapshot: preflightapi.VerificationSnapshot{
			Digest:       bundle.Manifest.SnapshotDigest,
			GitHead:      bundle.Manifest.Git.Head,
			GitBranch:    bundle.Manifest.Git.Branch,
			Dirty:        bundle.Manifest.Git.Dirty,
			ChangeMode:   bundle.Manifest.ChangeMode,
			ChangedFiles: append([]string(nil), bundle.Manifest.ChangedFiles...),
		},
		Coverage: preflightapi.VerificationCoverage{MatchedFiles: matched, UncoveredFiles: uncovered},
		Plan: preflightapi.VerificationPlan{
			Authority:    preflightapi.VerificationPlanAuthorityClientProposed,
			MatrixGroups: matrixGroups,
			Nodes:        nodes,
		},
	}, nil
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

type preparedVerificationSession struct {
	ProjectID    string
	Plans        []checkRunPlan
	WarmPlans    []checkRunPlan
	MatchedFiles []string
	BuildOptions syncbundlebuild.BuildOptions
	CoverageMode string
}

func prepareVerificationSession(ctx context.Context, options verificationSessionOptions, intent string) (preparedVerificationSession, error) {
	if err := validateExplicitDependencyWarmSecretRequest(options.CheckName, intent, options.RequestSecrets); err != nil {
		return preparedVerificationSession{}, cliConfigError(err.Error(), err)
	}
	loaded, err := loadProjectConfigOrInfer(".")
	if err != nil {
		return preparedVerificationSession{}, cliConfigError(err.Error(), err)
	}
	cfg := loaded.Config
	projectID := strings.TrimSpace(options.ProjectID)
	if projectID == "" {
		projectID = cfg.ProjectID
	}
	projectID, err = executionProjectID(options.APIURL, projectID)
	if err != nil {
		return preparedVerificationSession{}, err
	}
	resolve := agentResolveOptionsForIntent(intent, checkResolveOptions{
		APIURL: options.APIURL, ProjectID: projectID, Changes: options.Changes,
		RetryCount: options.RetryCount, RetryProvided: options.RetryProvided,
	})
	prepared := preparedVerificationSession{ProjectID: projectID, CoverageMode: "check"}
	if shouldUseAffectedRules(cfg, options.CheckName) && !agentIntentRequestsBroadVerification(intent, options.Goal) {
		selected, report, _, err := buildAgentAffectedRulePlans(ctx, ".", cfg, loaded.Path, resolve)
		if err != nil {
			return preparedVerificationSession{}, err
		}
		selected, report = agentAugmentAffectedPlansForIntent(cfg, selected, report, intent, resolve)
		selected = applyAgentAffectedPlanOptionsForIntent(intent, options.RequestSecrets, selected)
		warmSelected, err := agentDependencyWarmPlansForAffected(".", selected, cfg, resolve, intent)
		if err != nil {
			return preparedVerificationSession{}, err
		}
		for _, item := range warmSelected {
			prepared.WarmPlans = append(prepared.WarmPlans, item.Plan)
		}
		includeUntracked := report.Sync.IncludeUntracked
		sessionPlans := append(append(make([]affectedCheckPlan, 0, len(warmSelected)+len(selected)), warmSelected...), selected...)
		prepared.BuildOptions, err = compatibleAffectedSessionBuildOptions(sessionPlans, syncbundlebuild.BuildOptions{
			ChangeMode: report.ChangeMode, ExcludePatterns: append([]string(nil), report.Sync.ExcludePatterns...), IncludeUntracked: &includeUntracked,
		})
		if err != nil {
			return preparedVerificationSession{}, err
		}
		for _, item := range selected {
			prepared.Plans = append(prepared.Plans, item.Plan)
			for _, reason := range item.Reasons {
				prepared.MatchedFiles = append(prepared.MatchedFiles, reason.Path)
			}
		}
		prepared.CoverageMode = "affected_rules"
		return prepared, nil
	}
	recommendation, err := recommendAgentCheckWithIntent(cfg, options.CheckName, options.Goal, intent)
	if err != nil {
		return preparedVerificationSession{}, err
	}
	plan, err := resolveCheckRunPlan(recommendation.Check, cfg, resolve)
	if err != nil {
		return preparedVerificationSession{}, err
	}
	applyAgentRemoteRunOptionsForIntent(intent, options.RequestSecrets, &plan.Options)
	prepared.BuildOptions = syncbundlebuild.BuildOptions{
		ChangeMode: plan.Options.ChangeMode, ExcludePatterns: append([]string(nil), plan.Options.SyncExclude...), IncludeUntracked: plan.Options.IncludeUntracked,
	}
	if explicitDependencyWarmRequested(options.CheckName, intent) {
		targets, targetErr := dependencyWarmTargetsForExplicitCheck(ctx, ".", cfg, loaded.Path, resolve, plan)
		if targetErr != nil {
			return preparedVerificationSession{}, targetErr
		}
		prepared.WarmPlans, err = dependencyWarmPlansForExplicitCheck(plan, targets, cfg, resolve)
		if err != nil {
			return preparedVerificationSession{}, err
		}
		prepared.BuildOptions, err = explicitDependencyWarmSessionBuildOptions(prepared.WarmPlans, targets, prepared.BuildOptions)
		if err != nil {
			return preparedVerificationSession{}, err
		}
		return prepared, nil
	}
	prepared.Plans = []checkRunPlan{plan}
	return prepared, nil
}

func cancelVerificationSessionRejectedAfterCreation(
	client *api.Client,
	session preflightapi.VerificationSession,
	rejection error,
) (preflightapi.VerificationSession, error) {
	if client == nil || strings.TrimSpace(session.SessionID) == "" || session.State == preflightapi.VerificationStateTerminal {
		return session, rejection
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cancelled, err := client.CancelVerificationSession(cancelCtx, session.SessionID)
	cancel()
	if err == nil {
		return cancelled, rejection
	}
	var typed cliError
	if errors.As(rejection, &typed) {
		typed.Message += fmt.Sprintf("; automatic cancellation of unusable session %s failed: %v", session.SessionID, err)
		typed.Cause = errors.Join(rejection, err)
		return session, typed
	}
	return session, fmt.Errorf("%w; automatic cancellation of unusable session %s failed: %v", rejection, session.SessionID, err)
}

func reconcileAndCancelInterruptedVerificationSession(
	apiURL string,
	client *api.Client,
	request *preflightapi.CreateVerificationSessionRequest,
	session preflightapi.VerificationSession,
	received os.Signal,
	priorErr error,
) (preflightapi.VerificationSession, cliInterruptedError) {
	interrupted := cliInterruptedError{Signal: received, ResourceID: session.SessionID, APIURL: apiURL}
	if session.SessionID == "" && client != nil && request != nil {
		reconcileCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		reconciled, err := client.CreateVerificationSession(reconcileCtx, *request)
		cancel()
		if err != nil {
			interrupted.CancellationError = errors.Join(priorErr, fmt.Errorf("reconcile interrupted verification-session creation: %w", err))
			return session, interrupted
		}
		session = reconciled.Session
		interrupted.ResourceID = session.SessionID
	}
	if session.SessionID == "" {
		return session, interrupted
	}
	cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cancelled, err := client.CancelVerificationSession(cancelCtx, session.SessionID)
	cancel()
	acknowledged := err == nil
	interrupted.CancellationAcknowledged = &acknowledged
	interrupted.CancellationError = err
	if err == nil {
		session = cancelled
	}
	session.ClientInterruption = &preflightapi.VerificationClientInterruption{
		Signal:                   received.String(),
		ResourceID:               session.SessionID,
		CancellationAcknowledged: acknowledged,
		StatusCommand:            verificationSessionRecoveryCommand(apiURL, "get", session.SessionID),
		CancelCommand:            verificationSessionRecoveryCommand(apiURL, "cancel", session.SessionID),
	}
	return session, interrupted
}

func verificationSessionRecoveryCommand(apiURL, action, sessionID string) string {
	return "readyci sessions " + action + apiURLFlag(apiURL) + " " + shellQuoteArg(sessionID)
}

func verificationVerdictExit(session preflightapi.VerificationSession) error {
	if session.Currentness == preflightapi.VerificationCurrentnessStale {
		return exitCodeError{code: cliExitNonAuthoritative}
	}
	switch session.Verdict {
	case preflightapi.VerificationVerdictVerified, preflightapi.VerificationVerdictNoChangeRequired, preflightapi.VerificationVerdictNoVerificationNeeded:
		return nil
	case preflightapi.VerificationVerdictChecksPassed, preflightapi.VerificationVerdictInsufficientEvidence, preflightapi.VerificationVerdictStale:
		return exitCodeError{code: cliExitNonAuthoritative}
	default:
		return exitCodeError{code: 1}
	}
}

func printVerificationSession(session preflightapi.VerificationSession) {
	fmt.Printf("session: %s\nstate: %s\nverdict: %s\n", session.SessionID, session.State, session.Verdict)
	if session.Classification != "" {
		fmt.Printf("classification: %s\n", session.Classification)
	}
	for _, node := range session.Nodes {
		fmt.Printf("%s: %s\n", node.Name, node.Status)
	}
}
