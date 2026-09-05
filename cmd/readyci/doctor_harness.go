package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/config"
)

type repoHarnessReport struct {
	Status                string                   `json:"status"`
	AgentInstructions     []repoHarnessInstruction `json:"agent_instructions,omitempty"`
	EvidencePackChecks    []repoHarnessCheck       `json:"evidence_pack_checks,omitempty"`
	Findings              []repoHarnessFinding     `json:"findings,omitempty"`
	EvidencePackKinds     []string                 `json:"evidence_pack_kinds,omitempty"`
	AffectedRulesEnabled  bool                     `json:"affected_rules_enabled"`
	AgentInstructionsOkay bool                     `json:"agent_instructions_okay"`
	EvidencePacksOkay     bool                     `json:"evidence_packs_okay"`
}

type repoHarnessInstruction struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Lines   int    `json:"lines,omitempty"`
	ReadyCI bool   `json:"readyci"`
}

type repoHarnessCheck struct {
	Check         string   `json:"check"`
	Command       string   `json:"command,omitempty"`
	ArtifactPaths []string `json:"artifact_paths,omitempty"`
	EvidenceKinds []string `json:"evidence_kinds,omitempty"`
	detectionText string
}

type repoHarnessFinding struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Check   string `json:"check,omitempty"`
	Path    string `json:"path,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

func buildRepoHarnessReport(root string, cfg config.ProjectConfig) repoHarnessReport {
	report := repoHarnessReport{
		Status:               "ok",
		AffectedRulesEnabled: affectedAuditConfigured(cfg),
	}
	report.AgentInstructions = scanHarnessInstructions(root)
	report.AgentInstructionsOkay = harnessInstructionsOkay(report.AgentInstructions)
	if !report.AgentInstructionsOkay {
		report.addFinding("warn", "no portable Ready CI agent instructions were found", "", "", "run readyci agent setup or readyci agent setup --agents-md")
	}
	for _, instruction := range report.AgentInstructions {
		if instruction.Kind == "agents-md" && instruction.Lines > 160 {
			report.addFinding("warn", "AGENTS.md is large enough to crowd out task context", "", instruction.Path, "keep AGENTS.md as a map and move detailed policy into linked docs")
		}
	}
	if !report.AffectedRulesEnabled {
		report.addFinding("warn", "affected rules are not configured", "", "", "configure affected.rules or affected.profiles so agents can run the smallest relevant check")
	}
	report.EvidencePackChecks = harnessEvidenceChecks(cfg)
	report.EvidencePacksOkay = true
	for _, check := range report.EvidencePackChecks {
		report.EvidencePackKinds = uniqueStrings(append(report.EvidencePackKinds, check.EvidenceKinds...))
		if harnessCheckNeedsEvidence(check) && !harnessCheckHasRequiredEvidence(check) {
			report.EvidencePacksOkay = false
			report.addFinding("warn", "browser or UI check does not retain recognized evidence artifacts", check.Check, "", "add artifacts such as test-results/**, playwright-report/**, screenshots/**, videos/**, logs/**, or readyci.evidence.json")
		}
	}
	report.EvidencePackKinds = uniqueStrings(report.EvidencePackKinds)
	if len(report.Findings) > 0 {
		report.Status = "warn"
	}
	return report
}

func addHarnessDiagnostics(report *doctorReport, harness repoHarnessReport) {
	if len(harness.Findings) == 0 {
		report.ok("harness", "agent harness checks passed")
		return
	}
	for _, finding := range harness.Findings {
		var details []string
		if finding.Check != "" {
			details = append(details, "check: "+finding.Check)
		}
		if finding.Path != "" {
			details = append(details, "path: "+finding.Path)
		}
		var hints []string
		if finding.Hint != "" {
			hints = append(hints, finding.Hint)
		}
		report.addDiagnostic("harness", finding.Level, finding.Message, details, hints)
		switch finding.Level {
		case "fail":
			report.Summary.Failures++
		case "warn":
			report.Summary.Warnings++
		}
	}
}

func (report *repoHarnessReport) addFinding(level, message, check, path, hint string) {
	report.Findings = append(report.Findings, repoHarnessFinding{
		Level:   level,
		Message: message,
		Check:   check,
		Path:    path,
		Hint:    hint,
	})
}

func scanHarnessInstructions(root string) []repoHarnessInstruction {
	candidates := []repoHarnessInstruction{
		{Path: "AGENTS.md", Kind: "agents-md"},
		{Path: ".agents/skills/readyci-verify/SKILL.md", Kind: "codex-skill"},
		{Path: ".claude/skills/readyci-verify/SKILL.md", Kind: "claude-skill"},
		{Path: ".cursor/skills/readyci-verify/SKILL.md", Kind: "cursor-skill"},
	}
	var found []repoHarnessInstruction
	for _, candidate := range candidates {
		path := filepath.Join(root, filepath.FromSlash(candidate.Path))
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		candidate.Lines = countLines(string(data))
		candidate.ReadyCI = harnessInstructionMentionsAgentReadyCI(string(data))
		found = append(found, candidate)
	}
	return found
}

func harnessInstructionsOkay(instructions []repoHarnessInstruction) bool {
	for _, instruction := range instructions {
		if instruction.ReadyCI {
			return true
		}
	}
	return false
}

func harnessInstructionMentionsAgentReadyCI(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "readyci agent")
}

func harnessEvidenceChecks(cfg config.ProjectConfig) []repoHarnessCheck {
	var checks []repoHarnessCheck
	for _, name := range sortedCheckNames(cfg.Checks) {
		check := cfg.Checks[name]
		paths := uniqueNonEmptyStrings(append(append([]string(nil), cfg.Artifacts.Paths...), check.ArtifactPaths...))
		paths = uniqueNonEmptyStrings(append(paths, check.Outputs.Reviewable...))
		kinds := evidenceKindsForPaths(paths)
		detectionText := harnessCheckDetectionText(name, check)
		if !harnessCommandLooksAgentRelevant(name, detectionText) && len(kinds) == 0 {
			continue
		}
		checks = append(checks, repoHarnessCheck{
			Check:         name,
			Command:       check.Command,
			ArtifactPaths: paths,
			EvidenceKinds: kinds,
			detectionText: detectionText,
		})
	}
	return checks
}

func harnessCheckDetectionText(name string, check config.CheckConfig) string {
	parts := []string{name, check.Command}
	if browser := strings.TrimSpace(check.Browser); browser != "" {
		parts = append(parts, "browser "+browser)
	}
	parts = append(parts, check.Prepare...)
	for _, step := range check.Steps {
		parts = append(parts, step.Name, step.Command)
	}
	parts = append(parts, check.Post...)
	parts = append(parts, check.Always...)
	return strings.Join(uniqueNonEmptyStrings(parts), "\n")
}

func evidenceKindsForPaths(paths []string) []string {
	var kinds []string
	for _, path := range paths {
		kind, _, ok := evidencePackKindForPath(path)
		if ok {
			kinds = append(kinds, kind)
		}
	}
	return uniqueStrings(kinds)
}

func harnessCheckNeedsEvidence(check repoHarnessCheck) bool {
	return harnessCommandLooksAgentRelevant(check.Check, check.detectionText)
}

func harnessCheckHasRequiredEvidence(check repoHarnessCheck) bool {
	for _, kind := range check.EvidenceKinds {
		switch kind {
		case "readyci_evidence", "playwright_report", "playwright_trace", "screenshot", "video", "junit", "test_results", "logs":
			return true
		}
	}
	return false
}

func harnessCommandLooksAgentRelevant(name, command string) bool {
	text := strings.ToLower(name + "\n" + command)
	for _, needle := range []string{"e2e", "visual", "browser", "playwright", "cypress", "storybook", "selenium"} {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func countLines(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		count++
	}
	return count
}
