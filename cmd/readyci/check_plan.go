package main

import (
	"strings"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type checkResolveOptions struct {
	APIURL                   string
	ProjectID                string
	Region                   string
	Changes                  string
	NetworkMode              string
	NetworkAllowlist         string
	Timeout                  string
	Docker                   bool
	DockerProvided           bool
	KeepFailed               bool
	ConcurrencyGroup         string
	ConcurrencyGroupProvided bool
	ConcurrencyLimit         int
	ConcurrencyLimitProvided bool
	RetryCount               int
	RetryProvided            bool
	Open                     bool
	DashboardURL             string
	CachePaths               []string
}

type checkRunPlan struct {
	Name             string
	Options          remoteRunOptions
	Retries          int
	Matrix           config.MatrixConfig
	Variants         []matrixVariant
	Services         []string
	ServiceSpecs     []preflightapi.RunServiceSpec
	When             *checkWhenReport
	WhenConfig       config.WhenConfig
	RegionDefaulted  bool
	DiagnosticRegion string
}

type checkExecutionOptions struct {
	APIURL           string
	APIToken         string
	DryRun           bool
	JSONOutput       bool
	SummaryOutput    bool
	Why              bool
	Inferred         bool
	Signals          []string
	SelectionMode    string
	SelectedReason   string
	Watch            bool
	WatchInterval    time.Duration
	WarmDependencies bool
}

func checkPlanDiagnosticRegion(plan checkRunPlan) string {
	if strings.TrimSpace(plan.DiagnosticRegion) != "" {
		return preflightapi.NormalizeRegion(plan.DiagnosticRegion)
	}
	return preflightapi.NormalizeRegion(plan.Options.Region)
}
