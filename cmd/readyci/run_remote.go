package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type remoteRunOptions struct {
	CacheDependencyCheck     string
	CacheRuntimeRequirements map[string]string
	RuntimeRequirements      map[string]string
	APIURL                   string
	APIToken                 string
	ProjectID                string
	Region                   string
	IdempotencyKey           string
	RunGroupKey              string
	RunGroupKind             string
	RunGroupName             string
	RunGroupPredecessorID    string
	LogicalChildID           string
	LogicalChildName         string
	MatrixVariant            string
	Attempt                  int
	Command                  string
	CommandAlias             string
	Steps                    []preflightapi.RunStepSpec
	ConcurrencyGroup         string
	ConcurrencyLimit         int
	Env                      map[string]string
	PrepareCommands          []string
	PostCommands             []string
	AlwaysCommands           []string
	Image                    string
	RunnerSize               string
	TimeoutSeconds           int
	CacheMode                string
	CacheLaneSizeBytes       int64
	ArtifactPaths            []string
	NetworkMode              string
	NetworkAllowlist         []string
	Docker                   bool
	Services                 []preflightapi.RunServiceSpec
	WorkerSelector           map[string]string
	ChangeMode               string
	SecretsRequested         *bool
	SyncExclude              []string
	SyncReviewedFixtures     []syncbundle.ReviewedFixture
	IncludeUntracked         *bool
	CachePaths               []string
	CacheKeyFiles            []string
	KeepFailed               bool
	Open                     bool
	DashboardURL             string
	Progress                 agentProgressSink
	Detach                   bool
}

type remoteRunOutputMode int

const (
	remoteRunOutputHuman remoteRunOutputMode = iota
	remoteRunOutputSilent
	remoteRunOutputCollect
)

type remoteRunResult struct {
	RunID                           string
	RunGroupID                      string
	EventsURL                       string
	IdempotencyKey                  string
	Status                          string
	NetworkMode                     string
	TerminalEventType               string
	ExitCode                        int
	CommandExitCode                 int
	CommandExited                   bool
	ArtifactUploaded                bool
	FailedWorkspaceArtifactRetained bool
	FailedWorkspaceArtifactSkipped  bool
	LogTruncated                    bool
	FailureReason                   string
	Classification                  string
	Retryable                       bool
	AgentAction                     string
	FailureSummary                  string
	PrimaryFile                     string
	Events                          []preflightapi.RunEvent
	AnalysisEvents                  []preflightapi.RunEvent
	StreamRecovery                  *remoteRunStreamRecovery
	Detached                        bool
}

type remoteRunStreamRecovery struct {
	Reason         string
	StartCursor    int64
	LastCursor     int64
	SeenEvents     int64
	ReplayedEvents int
	Polls          int
}

var errRunStreamEndedBeforeTerminal = errors.New("run event stream ended before a terminal event")

const (
	runFailureAnalysisEventLimit = 24
	runFailureAnalysisDataLimit  = 8 * 1024
)

type runStreamEndedError struct {
	runID  string
	status string
}

func (e runStreamEndedError) Error() string {
	if e.runID == "" {
		return errRunStreamEndedBeforeTerminal.Error()
	}
	if e.status == "" {
		return fmt.Sprintf("%s for %s", errRunStreamEndedBeforeTerminal, e.runID)
	}
	return fmt.Sprintf("%s for %s while status was %s", errRunStreamEndedBeforeTerminal, e.runID, e.status)
}

func (e runStreamEndedError) Is(target error) bool {
	return target == errRunStreamEndedBeforeTerminal
}

func (e runStreamEndedError) Unwrap() error {
	return exitCodeError{code: 1}
}

type stringSliceFlag []string

func (flag *stringSliceFlag) String() string {
	if flag == nil {
		return ""
	}
	return strings.Join(*flag, ",")
}

func (flag *stringSliceFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return cliConfigError("empty value", nil)
	}
	*flag = append(*flag, value)
	return nil
}

func executeRemoteRun(options remoteRunOptions) error {
	result, err := executeRemoteRunInternal(options, remoteRunOutputHuman)
	if err != nil {
		redactor := newCLISecretRedactor([]string{"--token", options.APIToken})
		redactedErr := redactor.RedactError(err)
		if shouldPrintRunFailureDiagnosis(err) {
			printRunFailureDiagnosis(os.Stderr, redactedRemoteRunResult(result, redactor), redactedErr, redactor.Redact(remoteDisplayCommand(options)))
		}
		if shouldPrintFailedWorkspaceHint(options.KeepFailed, result.TerminalEventType, result.CommandExited, result.CommandExitCode, result.ArtifactUploaded, result.FailedWorkspaceArtifactRetained, result.FailedWorkspaceArtifactSkipped) {
			fmt.Fprintf(os.Stderr, "readyci: inspect failed workspace with `readyci artifacts download %s`\n", result.RunID)
		}
		return remoteRunAttemptError{result: result, command: redactor.Redact(remoteDisplayCommand(options)), err: redactedErr}
	}
	if result.Detached {
		fmt.Printf("run: %s\nstatus: %s\nnext: %s\n", result.RunID, result.Status, runRecoveryCommand(options.APIURL, "status", result.RunID))
		return nil
	}
	return exitCodeError{code: result.ExitCode}
}

func runRecoveryCommand(apiURL, action, runID string) string {
	return "readyci runs " + action + apiURLFlag(apiURL) + " " + shellQuoteArg(runID)
}

func redactedRemoteRunResult(result remoteRunResult, redactor cliSecretRedactor) remoteRunResult {
	result.FailureReason = redactor.Redact(result.FailureReason)
	result.FailureSummary = redactor.Redact(result.FailureSummary)
	result.PrimaryFile = redactor.Redact(result.PrimaryFile)
	result.Events = redactedRunEvents(result.Events, redactor)
	result.AnalysisEvents = redactedRunEvents(result.AnalysisEvents, redactor)
	return result
}

func redactedRunEvents(events []preflightapi.RunEvent, redactor cliSecretRedactor) []preflightapi.RunEvent {
	if len(events) == 0 {
		return events
	}
	redacted := append([]preflightapi.RunEvent(nil), events...)
	for index := range redacted {
		redacted[index].Data = redactor.Redact(redacted[index].Data)
	}
	return redacted
}

type remoteRunAttemptError struct {
	result  remoteRunResult
	command string
	err     error
}

func (e remoteRunAttemptError) Error() string {
	return e.err.Error()
}

func (e remoteRunAttemptError) Unwrap() error {
	return e.err
}

func shouldPrintRunFailureDiagnosis(err error) bool {
	return err != nil && !isSuccessfulExit(err) && !isLocalCancellationError(err)
}

func isLocalCancellationError(err error) bool {
	var interrupted cliInterruptedError
	return errors.As(err, &interrupted) || errors.Is(err, context.Canceled)
}
