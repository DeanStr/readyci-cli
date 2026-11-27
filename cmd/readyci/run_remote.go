package main

import (
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type remoteRunOptions struct {
	EnvironmentRequirements  []string
	CacheDependencyCheck     string
	CacheRuntimeRequirements map[string]string
	RuntimeRequirements      map[string]string
	APIURL                   string
	APIToken                 string
	ProjectID                string
	Region                   string
	IdempotencyKey           string
	RunGroupPredecessorID    string
	LogicalChildID           string
	LogicalChildName         string
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
	Detach                   bool
}

type remoteRunResult struct {
	RunID           string
	RunGroupID      string
	IdempotencyKey  string
	Status          string
	NetworkMode     string
	ExitCode        int
	CommandExitCode int
	CommandExited   bool
	FailureReason   string
	Classification  string
	Retryable       bool
	AgentAction     string
	FailureSummary  string
	PrimaryFile     string
	Events          []preflightapi.RunEvent
}

const runFailureAnalysisDataLimit = 8 * 1024

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

func runRecoveryCommand(apiURL, action, runID string) string {
	return "readyci runs " + action + apiURLFlag(apiURL) + " " + shellQuoteArg(runID)
}
