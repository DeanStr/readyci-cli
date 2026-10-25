package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func agentExplainCommand(args []string) error {
	fs := flag.NewFlagSet("agent explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apiURL, apiToken := apiFlags(fs)
	jsonOutput := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return cliConfigError(err.Error(), err)
	}
	if fs.NArg() != 1 {
		return cliConfigError("usage: readyci agent explain [--json] RUN_ID", nil)
	}
	client, err := authenticatedClient(*apiURL, *apiToken)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	report, err := buildAgentExplainReport(ctx, client, *apiURL, fs.Arg(0))
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSONOutput(report)
	}
	printAgentExplainReport(report)
	return nil
}

func buildAgentExplainReport(ctx context.Context, client *api.Client, apiURL, runID string) (agentExplainReport, error) {
	detail, err := client.GetRun(ctx, runID)
	if err != nil {
		return agentExplainReport{}, err
	}
	eventsResp, err := client.ListRunEvents(ctx, runID)
	if err != nil {
		return agentExplainReport{}, err
	}
	artifactsResp, artifactErr := client.ListArtifacts(ctx, runID)
	var warnings []agentWarning
	if artifactErr != nil {
		artifactsResp.Artifacts = nil
		warnings = append(warnings, agentWarning{
			Section: "artifacts",
			Message: "artifact listing failed: " + artifactErr.Error(),
		})
	}
	failureReason := terminalFailureReason(eventsResp.Events)
	status := effectiveRunStatus(detail.Summary.Status, eventsResp.Events)
	diagnosis := agentDiagnosisFromAnalysis(analyzeRemoteRunFailure(remoteRunResultFromDetailDiagnosis(detail, status, failureReason, eventsResp.Events), nil, detail.Spec.Command))
	reportWarnings := preflightapi.TestReportExecutionWarnings(status, detail.TestResults)
	warnings = append(warnings, reportWarnings...)
	if len(reportWarnings) > 0 {
		// Reconcile agent guidance while preserving the execution classification.
		diagnosis.Action = preflightapi.AgentActionInspectTestReport
		diagnosis.Summary = preflightapi.TestReportConflictMessage
	}
	if detail.TestResults != nil && detail.TestResults.Collection.RequiredOmissions() {
		collection := detail.TestResults.Collection
		warnings = append(warnings, agentWarning{
			Code: "artifact_collection_incomplete", Section: "test_results",
			Message: fmt.Sprintf("Artifact collection was incomplete: %d files were not retained; omitted report candidates: %d. Reported counts cover retained reports only.", collection.OmittedFileCount, collection.OmittedReportCount),
			Action:  preflightapi.AgentActionInspectTestReport, RunID: runID,
			ArtifactID: detail.TestResults.ArtifactID, ArtifactSHA256: detail.TestResults.ArtifactSHA256,
		})
	}
	evidence := evidenceForDiagnosis(diagnosis, eventsResp.Events, 8)
	checkName, matrixVariant := preflightapi.SplitCheckAliasVariant(detail.Spec.CommandAlias)
	if checkName == "" {
		checkName = detail.Spec.CommandAlias
	}
	requestSecrets := persistedRunSecretsRequested(detail.Spec.SecretsRequested)
	rerunCommand := rerunCommandForDiagnosis(status, diagnosis, apiURL, checkName, requestSecrets)
	logsCommand := readyCILogsCommand(apiURL, runID)
	evidencePacks, evidencePackErr := evidencePacksFromRunArtifactFiles(ctx, client, apiURL, runID, detail.Spec.ArtifactPaths, artifactsResp.Artifacts)
	if evidencePackErr != nil {
		warnings = append(warnings, agentWarning{
			Section: "artifacts",
			Message: "artifact evidence inspection failed: " + evidencePackErr.Error(),
		})
	}
	repair := buildAgentRepairPacket(diagnosis, detail.Spec.Command, rerunCommand, logsCommand, evidence, evidencePacks, detail.PolicyReasons)
	if advisory := sigstorePrerequisiteAdvisory(status, diagnosis.Kind, detail.Spec.NetworkMode, eventsResp.Events); advisory != "" {
		warnings = append(warnings, agentWarning{Section: "test_prerequisite", Message: advisory})
		repair.RootCause += " " + advisory
	}
	for _, advisory := range kubernetesPrerequisiteAdvisories(status, eventsResp.Events) {
		warnings = append(warnings, agentWarning{Section: "test_prerequisite", Message: advisory})
		repair.RootCause += " " + advisory
	}
	var testResults *preflightapi.RunTestResults
	if detail.TestResults != nil {
		failures := detail.TestResults.Failures(50)
		testResults = &failures
		if len(failures.Results) > 0 {
			first := failures.Results[0]
			repair.FailingTest = first.Test
			if repair.PrimaryFile == "" {
				repair.PrimaryFile = first.File
			}
		}
	}
	runtimeDiagnostics := preflightapi.RuntimeDiagnosticsFromEvents(eventsResp.Events)
	nextStep := recommendedNextStep(diagnosis, apiURL, checkName, requestSecrets)
	if runtimeDiagnostics != nil && diagnosis.Kind == preflightapi.ClassificationWorkerInternalError {
		nextStep = "Inspect runtime_diagnostics and retained logs to identify the runtime failure before retrying or changing repository code."
	}
	return agentExplainReport{
		RuntimeDiagnostics:  runtimeDiagnostics,
		Timings:             detail.Timings,
		TestResults:         testResults,
		SchemaVersion:       agentSchemaVersion,
		RunID:               detail.Summary.RunID,
		Status:              status,
		Terminal:            preflightapi.IsTerminalRunStatus(status),
		ExitCode:            exitCodeForRunDetail(detail, eventsResp.Events),
		Check:               checkName,
		MatrixVariant:       matrixVariant,
		Command:             detail.Spec.Command,
		Diagnosis:           diagnosis,
		Repair:              repair,
		Evidence:            evidence,
		EvidencePacks:       evidencePacks,
		RecommendedNextStep: nextStep,
		RerunCommand:        rerunCommand,
		LogsCommand:         logsCommand,
		Artifacts:           artifactsResp.Artifacts,
		PolicyReasons:       append([]string(nil), detail.PolicyReasons...),
		Warnings:            warnings,
		LogTruncated:        eventsResp.Truncated || eventsWereTruncated(eventsResp.Events),
		Spec:                detail.Spec,
	}, nil
}
