package main

import (
	"fmt"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type repoSignal struct {
	Kind       string   `json:"kind"`
	Path       string   `json:"path,omitempty"`
	Confidence float64  `json:"confidence"`
	Evidence   []string `json:"evidence,omitempty"`
}

type costEstimate struct {
	RunnerSize     string `json:"runner_size"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type runNeeds struct {
	NetworkMode string   `json:"network_mode"`
	Docker      bool     `json:"docker"`
	Services    []string `json:"services,omitempty"`
	CacheMode   string   `json:"cache_mode,omitempty"`
}

type checkCandidate struct {
	Name        string       `json:"name"`
	Command     string       `json:"command,omitempty"`
	Reason      string       `json:"reason,omitempty"`
	Confidence  float64      `json:"confidence"`
	Cost        costEstimate `json:"cost"`
	RiskCovered []string     `json:"risk_covered,omitempty"`
	Needs       runNeeds     `json:"needs"`
}

type skippedCheck struct {
	Name         string   `json:"name"`
	Reason       string   `json:"reason"`
	MatchedPaths []string `json:"matched_paths,omitempty"`
	SubsumedBy   []string `json:"subsumed_by,omitempty"`
}

type verificationPlan struct {
	Goal            string           `json:"goal"`
	Intent          string           `json:"intent"`
	Mode            string           `json:"mode"`
	ConfigPath      string           `json:"config_path"`
	Inferred        bool             `json:"inferred"`
	Signals         []repoSignal     `json:"signals,omitempty"`
	Warnings        []string         `json:"warnings,omitempty"`
	Candidates      []checkCandidate `json:"candidates,omitempty"`
	Selected        []checkCandidate `json:"selected"`
	Skipped         []skippedCheck   `json:"skipped,omitempty"`
	Explanation     []string         `json:"explanation"`
	Confidence      float64          `json:"confidence"`
	RecommendedNext []string         `json:"recommended_next,omitempty"`
}

type verificationPlanOptions struct {
	Goal           string
	Intent         string
	Mode           string
	Inferred       bool
	Signals        []string
	Warnings       []string
	SelectedReason string
}

func buildCheckVerificationPlan(cfg config.ProjectConfig, configPath string, plan checkRunPlan, options verificationPlanOptions) verificationPlan {
	goal := defaultString(strings.TrimSpace(options.Goal), "default")
	intent := defaultString(strings.TrimSpace(options.Intent), agentIntentEditLoop)
	mode := defaultString(strings.TrimSpace(options.Mode), "check")
	selectedReason := strings.TrimSpace(options.SelectedReason)
	if selectedReason == "" {
		selectedReason = "Selected configured check " + plan.Name + "."
	}
	selected := checkCandidateFromRunPlan(plan, selectedReason, confidenceForMode(mode, options.Inferred, 1, 0))
	explanation := []string{selectedReason}
	if options.Inferred {
		explanation = append(explanation, "No Ready CI config was found, so Ready CI used repository signals to build an in-memory plan.")
	}
	if plan.Options.NetworkMode != "" {
		explanation = append(explanation, "Run uses network mode "+plan.Options.NetworkMode+".")
	}
	if len(plan.Services) > 0 {
		explanation = append(explanation, "Run needs service(s): "+strings.Join(plan.Services, ", ")+".")
	}
	return verificationPlan{
		Goal:            goal,
		Intent:          intent,
		Mode:            mode,
		ConfigPath:      configPath,
		Inferred:        options.Inferred,
		Signals:         repoSignalsFromStrings(options.Signals),
		Warnings:        append([]string(nil), options.Warnings...),
		Candidates:      checkCandidatesFromConfig(cfg, plan.Name),
		Selected:        []checkCandidate{selected},
		Explanation:     explanation,
		Confidence:      selected.Confidence,
		RecommendedNext: recommendedNextForCheckPlan(plan),
	}
}

func buildAffectedVerificationPlan(cfg config.ProjectConfig, configPath string, report affectedDryRunReport, options verificationPlanOptions) verificationPlan {
	goal := defaultString(strings.TrimSpace(options.Goal), "default")
	intent := defaultString(strings.TrimSpace(options.Intent), agentIntentEditLoop)
	mode := defaultString(strings.TrimSpace(options.Mode), "affected_rules")
	selected := make([]checkCandidate, 0, len(report.SelectedChecks))
	explanation := make([]string, 0, len(report.SelectedChecks)+4)
	for _, check := range report.SelectedChecks {
		reason := affectedDryRunCheckReason(check)
		selected = append(selected, checkCandidateFromAffectedDryRun(check, reason, confidenceForMode(mode, options.Inferred, len(report.SelectedChecks), len(report.ChangedFiles))))
		explanation = append(explanation, reason)
	}
	skipped := make([]skippedCheck, 0, len(report.SkippedChecks))
	for _, check := range report.SkippedChecks {
		reason := check.Reason
		if len(check.SubsumedBy) > 0 {
			reason = "subsumed by " + strings.Join(check.SubsumedBy, ", ")
		}
		skipped = append(skipped, skippedCheck{
			Name:         check.Name,
			Reason:       reason,
			MatchedPaths: append([]string(nil), check.MatchedPaths...),
			SubsumedBy:   append([]string(nil), check.SubsumedBy...),
		})
	}
	if len(report.ChangedFiles) == 0 {
		explanation = append(explanation, "No changed files were found for change mode "+report.ChangeMode+".")
	}
	if len(selected) == 0 && len(report.ChangedFiles) > 0 {
		explanation = append(explanation, "No changed files matched affected rules.")
	}
	if len(selected) == 0 && len(skipped) == 0 && len(explanation) == 0 {
		explanation = append(explanation, "No checks were selected.")
	}
	if options.Inferred {
		explanation = append(explanation, "No Ready CI config was found, so Ready CI used repository signals to build an in-memory plan.")
	}
	confidence := confidenceForMode(mode, options.Inferred, len(selected), len(report.ChangedFiles))
	return verificationPlan{
		Goal:            goal,
		Intent:          intent,
		Mode:            mode,
		ConfigPath:      configPath,
		Inferred:        options.Inferred,
		Signals:         repoSignalsFromStrings(options.Signals),
		Warnings:        append([]string(nil), options.Warnings...),
		Candidates:      checkCandidatesFromConfig(cfg, ""),
		Selected:        selected,
		Skipped:         skipped,
		Explanation:     explanation,
		Confidence:      confidence,
		RecommendedNext: recommendedNextForAffectedPlan(report),
	}
}

func repoSignalsFromStrings(values []string) []repoSignal {
	if len(values) == 0 {
		return nil
	}
	signals := make([]repoSignal, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		signals = append(signals, repoSignal{
			Kind:       "repo_signal",
			Confidence: 0.72,
			Evidence:   []string{value},
		})
	}
	return signals
}

func checkCandidatesFromConfig(cfg config.ProjectConfig, selectedName string) []checkCandidate {
	names := sortedCheckNames(cfg.Checks)
	candidates := make([]checkCandidate, 0, len(names))
	for _, name := range names {
		check := cfg.Checks[name]
		reason := "Configured Ready CI check."
		confidence := 0.55
		if name == selectedName {
			reason = "Selected Ready CI check."
			confidence = 0.82
		}
		if plan, err := resolveCheckRunPlan(name, cfg, checkResolveOptions{RetryCount: -1}); err == nil {
			candidates = append(candidates, checkCandidateFromRunPlan(plan, reason, confidence))
			continue
		}
		runnerSize := cfg.RunnerSize
		if check.Size != "" {
			runnerSize = check.Size
		}
		if check.RunnerSize != "" {
			runnerSize = check.RunnerSize
		}
		timeoutSeconds, _ := parseTimeoutSeconds(defaultInt(check.TimeoutSeconds, cfg.TimeoutSeconds), defaultString(check.Timeout, cfg.Timeout))
		networkMode := check.NetworkMode
		if networkMode == "" {
			networkMode = cfg.Network.Mode
		}
		if networkMode == "" {
			networkMode = preflightapi.DefaultNetworkMode
		}
		cacheMode := projectCacheMode(cfg)
		if check.CacheMode != "" {
			cacheMode = check.CacheMode
		}
		if check.Cache.Mode != "" {
			cacheMode = check.Cache.Mode
		}
		candidates = append(candidates, checkCandidate{
			Name:       name,
			Command:    check.Command,
			Reason:     reason,
			Confidence: confidence,
			Cost: costEstimate{
				RunnerSize:     defaultString(runnerSize, preflightapi.DefaultRunnerSize),
				TimeoutSeconds: defaultInt(timeoutSeconds, defaultRunTimeoutSeconds),
			},
			RiskCovered: risksForCheckName(name),
			Needs: runNeeds{
				NetworkMode: networkMode,
				Docker:      boolValue(check.Docker),
				Services:    append([]string(nil), check.Services...),
				CacheMode:   cacheMode,
			},
		})
	}
	return candidates
}

func checkCandidateFromRunPlan(plan checkRunPlan, reason string, confidence float64) checkCandidate {
	return checkCandidate{
		Name:       plan.Name,
		Command:    plan.Options.Command,
		Reason:     reason,
		Confidence: confidence,
		Cost: costEstimate{
			RunnerSize:     defaultString(plan.Options.RunnerSize, preflightapi.DefaultRunnerSize),
			TimeoutSeconds: defaultInt(plan.Options.TimeoutSeconds, defaultRunTimeoutSeconds),
		},
		RiskCovered: risksForCheckName(plan.Name),
		Needs: runNeeds{
			NetworkMode: plan.Options.NetworkMode,
			Docker:      plan.Options.Docker,
			Services:    append([]string(nil), plan.Services...),
			CacheMode:   plan.Options.CacheMode,
		},
	}
}

func checkCandidateFromAffectedDryRun(check affectedDryRunCheck, reason string, confidence float64) checkCandidate {
	return checkCandidate{
		Name:       check.Name,
		Command:    check.Command,
		Reason:     reason,
		Confidence: confidence,
		Cost: costEstimate{
			RunnerSize:     defaultString(check.RunnerSize, preflightapi.DefaultRunnerSize),
			TimeoutSeconds: defaultInt(check.TimeoutSeconds, defaultRunTimeoutSeconds),
		},
		RiskCovered: risksForCheckName(check.Name),
		Needs: runNeeds{
			NetworkMode: check.NetworkMode,
			Docker:      check.Docker,
			Services:    append([]string(nil), check.Services...),
			CacheMode:   check.CacheMode,
		},
	}
}

func affectedDryRunCheckReason(check affectedDryRunCheck) string {
	if len(check.Reasons) == 0 {
		return "Selected " + check.Name + " from affected rules."
	}
	parts := make([]string, 0, len(check.Reasons))
	for _, reason := range check.Reasons {
		parts = append(parts, affectedRuleMatchDescription(reason))
	}
	return "Selected " + check.Name + " because " + strings.Join(parts, "; ") + "."
}

func confidenceForMode(mode string, inferred bool, selectedCount, changedFileCount int) float64 {
	switch mode {
	case "explicit_check":
		return 0.98
	case "affected_rules":
		if selectedCount > 0 {
			if inferred {
				return 0.78
			}
			return 0.9
		}
		if changedFileCount == 0 {
			return 0.86
		}
		return 0.58
	case "inferred_fallback":
		return 0.68
	default:
		if inferred {
			return 0.7
		}
		return 0.76
	}
}

func risksForCheckName(name string) []string {
	lower := strings.ToLower(name)
	var risks []string
	switch {
	case strings.Contains(lower, "e2e"), strings.Contains(lower, "playwright"), strings.Contains(lower, "browser"):
		risks = append(risks, "browser_flow")
	case strings.Contains(lower, "migration"):
		risks = append(risks, "database_schema")
	case strings.Contains(lower, "shell"), strings.Contains(lower, "deploy"):
		risks = append(risks, "script_syntax")
	case strings.Contains(lower, "lint"):
		risks = append(risks, "lint")
	case strings.Contains(lower, "type"):
		risks = append(risks, "typecheck")
	case strings.Contains(lower, "vet"):
		risks = append(risks, "static_analysis")
	case strings.Contains(lower, "vuln"):
		risks = append(risks, "dependency_vulnerability")
	case strings.Contains(lower, "web"):
		risks = append(risks, "frontend")
	case strings.Contains(lower, "go"), strings.Contains(lower, "cli"), strings.Contains(lower, "platform"):
		risks = append(risks, "go_tests")
	}
	if len(risks) == 0 {
		risks = append(risks, "verification")
	}
	return risks
}

func recommendedNextForCheckPlan(plan checkRunPlan) []string {
	if strings.Contains(strings.ToLower(plan.Name), "web") && !strings.Contains(strings.ToLower(plan.Name), "e2e") {
		return []string{"Run a browser/e2e check before merging if this change touches routing, auth, or browser-only behavior."}
	}
	if plan.Options.NetworkMode == "none" && len(plan.Options.NetworkAllowlist) == 0 {
		return []string{"If dependency installation fails on network policy, warm dependencies or add the narrowest phase-specific network allowance available."}
	}
	return nil
}

func recommendedNextForAffectedPlan(report affectedDryRunReport) []string {
	if len(report.SelectedChecks) > 0 {
		return nil
	}
	if len(report.ChangedFiles) == 0 {
		return []string{"No verification run is needed for an empty changed-file set."}
	}
	return []string{"Review affected rules or run a broader check if these changed files should be covered."}
}

func printVerificationPlan(plan verificationPlan) {
	fmt.Println("verification: plan")
	fmt.Printf("goal: %s\n", plan.Goal)
	fmt.Printf("mode: %s\n", plan.Mode)
	fmt.Printf("confidence: %.2f\n", plan.Confidence)
	printDryRunWarnings(plan.Warnings)
	if len(plan.Signals) > 0 {
		fmt.Println("signals:")
		for _, signal := range plan.Signals {
			for _, evidence := range signal.Evidence {
				fmt.Printf("  - %s\n", evidence)
			}
		}
	}
	if len(plan.Selected) == 0 {
		fmt.Println("selected: none")
	} else {
		fmt.Println("selected:")
		for _, selected := range plan.Selected {
			fmt.Printf("  - %s", selected.Name)
			if selected.Reason != "" {
				fmt.Printf(": %s", selected.Reason)
			}
			fmt.Println()
		}
	}
	if len(plan.Skipped) > 0 {
		fmt.Println("skipped:")
		for _, skipped := range plan.Skipped {
			fmt.Printf("  - %s: %s\n", skipped.Name, skipped.Reason)
		}
	}
	if len(plan.Explanation) > 0 {
		fmt.Println("why:")
		for _, reason := range plan.Explanation {
			fmt.Printf("  - %s\n", reason)
		}
	}
	for _, next := range plan.RecommendedNext {
		fmt.Printf("recommended_next: %s\n", next)
	}
}
