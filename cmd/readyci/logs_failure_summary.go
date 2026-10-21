package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/diagnosiscommon"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func buildRunLogsFailureSummaryReport(detail preflightapi.RunDetail, events []preflightapi.RunEvent, truncated bool) runLogsFailureSummaryReport {
	summary := detail.Summary
	analysisEvents := events
	if summary.Classification == preflightapi.ClassificationTestFailed && diagnosiscommon.PythonWarningDiagnostic(summary.FailureSummary) {
		analysisEvents = repositoryTestOutputEvents(events)
	}
	analysis := localAnalyzeRunFailureWithNetworkMode(summary.Status, detail.Spec.Command, summary.ExitCode, analysisEvents, summary.FailureSummary, detail.Spec.NetworkMode)
	classification := analysis.Classification
	if summary.Classification != "" {
		classification = summary.Classification
	}
	failureSummary := ""
	if runLogsClassificationIsFailure(classification) {
		failureSummary = strings.TrimSpace(summary.FailureSummary)
	}
	if failureSummary == "" && runLogsClassificationIsFailure(analysis.Classification) {
		failureSummary = strings.TrimSpace(analysis.Summary)
	}
	if refined, ok := refinedObservedTestFailureSummary(classification, failureSummary, analysis); ok {
		failureSummary = refined
	}
	var evidence []preflightapi.RunFailureEvidence
	if runLogsClassificationIsFailure(classification) || runLogsClassificationIsFailure(analysis.Classification) {
		evidence = selectRunLogsFailureEvidence(analysis.Evidence, 12)
	}
	primaryFile := ""
	if runLogsClassificationIsFailure(classification) {
		primaryFile = preferredRunLogsPrimaryFile(summary.PrimaryFile, analysis.PrimaryFile, evidence)
	}
	agentAction := strings.TrimSpace(summary.AgentAction)
	if agentAction == "" {
		agentAction = strings.TrimSpace(analysis.AgentAction)
	}
	retryable := analysis.Retryable
	if summary.Classification != "" {
		retryable = summary.Retryable
	}
	return runLogsFailureSummaryReport{
		RunID:          defaultString(summary.RunID, detail.Spec.RunID),
		Status:         summary.Status,
		Classification: classification,
		Confidence:     analysis.Confidence,
		Retryable:      retryable,
		AgentAction:    agentAction,
		FailureSummary: failureSummary,
		PrimaryFile:    primaryFile,
		Evidence:       evidence,
		Truncated:      truncated || eventsWereTruncated(events),
	}
}

func runLogsClassificationIsFailure(classification string) bool {
	switch classification {
	case "", preflightapi.ClassificationSuccess, preflightapi.ClassificationPending:
		return false
	default:
		return true
	}
}

func selectRunLogsFailureEvidence(evidence []preflightapi.RunFailureEvidence, limit int) []preflightapi.RunFailureEvidence {
	return selectRunLogsFailureEvidenceWithRanker(evidence, limit, &runLogsFailureEvidenceRanker{})
}

type runLogsFailureEvidenceRankKey struct {
	text string
	path string
}

type runLogsFailureEvidenceRank struct {
	keep  bool
	score int
}

type runLogsFailureEvidenceRanker struct {
	ranks map[runLogsFailureEvidenceRankKey]runLogsFailureEvidenceRank
	score func(preflightapi.RunFailureEvidence) int
}

func runLogsFailureEvidenceKey(item preflightapi.RunFailureEvidence) runLogsFailureEvidenceRankKey {
	return runLogsFailureEvidenceRankKey{text: strings.TrimSpace(item.Text), path: item.Path}
}

func (ranker *runLogsFailureEvidenceRanker) rank(item preflightapi.RunFailureEvidence) runLogsFailureEvidenceRank {
	key := runLogsFailureEvidenceKey(item)
	if rank, ok := ranker.ranks[key]; ok {
		return rank
	}
	if ranker.ranks == nil {
		ranker.ranks = make(map[runLogsFailureEvidenceRankKey]runLogsFailureEvidenceRank)
	}
	rank := runLogsFailureEvidenceRank{keep: key.text != "" && !lowSignalRunLogsFailureEvidence(key.text)}
	if rank.keep {
		score := ranker.score
		if score == nil {
			score = runLogsFailureEvidenceScore
		}
		rank.score = score(item)
	}
	ranker.ranks[key] = rank
	return rank
}

func (ranker *runLogsFailureEvidenceRanker) retain(evidence []preflightapi.RunFailureEvidence) {
	// Keep only the collector's bounded retained candidates, not metadata
	// for every line of an unbounded repository log.
	ranks := make(map[runLogsFailureEvidenceRankKey]runLogsFailureEvidenceRank, len(evidence))
	for _, item := range evidence {
		key := runLogsFailureEvidenceKey(item)
		if rank, ok := ranker.ranks[key]; ok {
			ranks[key] = rank
		}
	}
	ranker.ranks = ranks
}

func selectRunLogsFailureEvidenceWithRanker(evidence []preflightapi.RunFailureEvidence, limit int, ranker *runLogsFailureEvidenceRanker) []preflightapi.RunFailureEvidence {
	if limit <= 0 {
		limit = len(evidence)
	}
	type rankedEvidence struct {
		item  preflightapi.RunFailureEvidence
		score int
	}
	candidates := make([]rankedEvidence, 0, len(evidence))
	for _, item := range evidence {
		rank := ranker.rank(item)
		if !rank.keep {
			continue
		}
		candidates = append(candidates, rankedEvidence{item: item, score: rank.score})
	}
	// Text/path and their rank are immutable. Never repeat diagnostic
	// regular expressions from the stable-sort comparator.
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})
	ordered := make([]preflightapi.RunFailureEvidence, len(candidates))
	for index, candidate := range candidates {
		ordered[index] = candidate.item
	}
	selected := dedupeRunLogsFailureEvidence(ordered, limit)
	if len(selected) > 0 {
		return selected
	}
	return dedupeRunLogsFailureEvidence(evidence, limit)
}

func runLogsFailureEvidenceScore(item preflightapi.RunFailureEvidence) int {
	text := strings.TrimSpace(item.Text)
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "modulenotfounderror") || strings.Contains(lower, "no module named"):
		return 100
	case strings.HasPrefix(lower, "--- fail:"):
		return 98
	case localDiagnosisPytestFailedPattern.MatchString(text) || localDiagnosisAssertionPattern.MatchString(lower):
		return 95
	case diagnosiscommon.PythonExceptionDiagnostic(text):
		return 94
	case diagnosiscommon.PythonWarningDiagnostic(text):
		return 5
	case runLogsProjectRelativePath(item.Path):
		return 90
	case localAnalysisTextLooksTypecheckFailure(text, lower):
		return 85
	case strings.TrimSpace(item.Path) != "":
		return 70
	case strings.Contains(lower, "importerror") || strings.Contains(lower, "error collecting"):
		return 60
	default:
		return 10
	}
}

func preferredRunLogsPrimaryFile(summaryPrimary, analysisPrimary string, evidence []preflightapi.RunFailureEvidence) string {
	for _, item := range evidence {
		if runLogsProjectRelativePath(item.Path) {
			return strings.TrimSpace(item.Path)
		}
	}
	if runLogsProjectRelativePath(analysisPrimary) {
		return strings.TrimSpace(analysisPrimary)
	}
	if runLogsProjectRelativePath(summaryPrimary) {
		return strings.TrimSpace(summaryPrimary)
	}
	if strings.TrimSpace(analysisPrimary) != "" {
		return strings.TrimSpace(analysisPrimary)
	}
	return strings.TrimSpace(summaryPrimary)
}

func runLogsProjectRelativePath(path string) bool {
	path = strings.TrimSpace(path)
	return path != "" &&
		!strings.HasPrefix(path, "/") &&
		!strings.HasPrefix(path, ".") &&
		!strings.Contains(path, "..")
}

func lowSignalRunLogsFailureEvidence(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return true
	}
	if localLowSignalEvidenceText(lower) {
		return true
	}
	return strings.HasPrefix(lower, "readyci: retained failed workspace") ||
		strings.HasPrefix(lower, "readyci: retained failed workspace artifact") ||
		strings.HasPrefix(lower, "readyci: cache filesystem usage") ||
		strings.HasPrefix(lower, "readyci: cache top-level usage") ||
		strings.HasPrefix(lower, "filesystem ") ||
		strings.Contains(lower, "/cache/failed-runs")
}

func dedupeRunLogsFailureEvidence(evidence []preflightapi.RunFailureEvidence, limit int) []preflightapi.RunFailureEvidence {
	selected := make([]preflightapi.RunFailureEvidence, 0, min(limit, len(evidence)))
	seen := make(map[string]struct{}, len(evidence))
	for _, item := range evidence {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		key := strings.ToLower(item.Stream + "\x00" + item.Path + "\x00" + text)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		item.Text = text
		selected = append(selected, item)
		if len(selected) >= limit {
			return selected
		}
	}
	return selected
}

func writeHumanRunLogsFailureSummary(report runLogsFailureSummaryReport) error {
	lines := []string{
		"run_id: " + sanitizeRunLogsSummaryText(report.RunID),
		"status: " + sanitizeRunLogsSummaryText(report.Status),
	}
	if report.Classification != "" {
		lines = append(lines, "classification: "+sanitizeRunLogsSummaryText(report.Classification))
	}
	if report.AgentAction != "" {
		lines = append(lines, "agent_action: "+sanitizeRunLogsSummaryText(report.AgentAction))
	}
	if report.FailureSummary != "" {
		lines = append(lines, "failure_summary: "+sanitizeRunLogsSummaryText(report.FailureSummary))
	}
	if report.PrimaryFile != "" {
		lines = append(lines, "primary_file: "+sanitizeRunLogsSummaryText(report.PrimaryFile))
	}
	if report.Truncated {
		lines = append(lines, "truncated: true")
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(os.Stdout, line); err != nil {
			return err
		}
	}
	if len(report.Evidence) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(os.Stdout, "evidence:"); err != nil {
		return err
	}
	for _, item := range report.Evidence {
		prefix := sanitizeRunLogsSummaryText(item.Stream)
		if item.Line > 0 {
			prefix = fmt.Sprintf("%s:%d", defaultString(prefix, "event"), item.Line)
		} else {
			prefix = defaultString(prefix, "event")
		}
		if item.Path != "" {
			prefix += " " + sanitizeRunLogsSummaryText(item.Path)
		}
		if _, err := fmt.Fprintf(os.Stdout, "  %s: %s\n", prefix, sanitizeRunLogsSummaryText(item.Text)); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeRunLogsSummaryText(value string) string {
	value = sanitizeLogDisplayText(value)
	value = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(value)
	return strings.TrimSpace(value)
}
