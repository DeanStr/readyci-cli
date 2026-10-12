package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/artifactbundle"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

func TestAgentJSONContractGoldenSnapshots(t *testing.T) {
	reports := map[string]any{
		"plan":           agentContractPlanReport(),
		"verify":         agentContractVerifyReport(),
		"resume":         agentContractResumeReport(),
		"explain":        agentContractExplainReport(),
		"artifact_plan":  agentContractArtifactPlanReport(),
		"artifact_apply": agentContractArtifactApplyReport(),
		"doctor":         agentContractInstructionsDoctorReport(),
		"skipped":        agentContractSkippedReport(),
		"error":          agentCommandErrorReport(preflightapi.ClassificationConfigInvalid, errors.New("invalid Ready CI config")),
	}
	for name, report := range reports {
		t.Run(name, func(t *testing.T) {
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				t.Fatalf("marshal %s agent report: %v", name, err)
			}
			data = append(data, '\n')
			want, err := os.ReadFile(filepath.Join("testdata", "agent_contracts", name+".golden.json"))
			if err != nil {
				t.Fatalf("read %s golden: %v", name, err)
			}
			if string(data) != string(want) {
				t.Fatalf("%s agent contract drifted:\n%s", name, data)
			}
			assertAgentSchemaVersion(t, data)
			validateAgentJSONSchema(t, want)
		})
	}
}

func TestAgentJSONSchemaDocument(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "schema", "readyci.verification.v0alpha1.schema.json"))
	if err != nil {
		t.Fatalf("read agent schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse agent schema: %v", err)
	}
	if schema["$id"] != "https://readyci.com/schema/readyci.verification.v0alpha1.schema.json" {
		t.Fatalf("schema id = %v, want readyci agent schema id", schema["$id"])
	}
	oneOf, ok := schema["oneOf"].([]any)
	if !ok {
		t.Fatal("schema missing top-level oneOf")
	}
	refs := make([]string, 0, len(oneOf))
	for _, item := range oneOf {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("schema oneOf entry = %T, want object", item)
		}
		if ref, ok := entry["$ref"].(string); ok {
			refs = append(refs, ref)
		}
	}
	if !contains(refs, "#/$defs/agentInstructionsDoctorReport") {
		t.Fatalf("schema oneOf refs %v missing instructions doctor report", refs)
	}
	defs, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("schema missing $defs")
	}
	recipe, ok := defs["verificationRecipe"].(map[string]any)
	if !ok {
		t.Fatal("schema missing verificationRecipe")
	}
	required, ok := recipe["required"].([]any)
	if !ok || !containsAnyString(required, "digest") {
		t.Fatalf("verification recipe required fields = %v, want digest", recipe["required"])
	}
	intent, ok := defs["intent"].(map[string]any)
	if !ok {
		t.Fatal("schema missing intent enum")
	}
	rawValues, ok := intent["enum"].([]any)
	if !ok {
		t.Fatal("schema intent is not an enum")
	}
	values := make([]string, 0, len(rawValues))
	for _, value := range rawValues {
		values = append(values, value.(string))
	}
	for _, want := range agentIntentValues {
		if !contains(values, want) {
			t.Fatalf("schema intent enum %v missing %s", values, want)
		}
	}
	if !strings.Contains(string(data), "repair") || !strings.Contains(string(data), "policy") || !strings.Contains(string(data), "agentInstructionsDoctorReport") {
		t.Fatal("schema should document repair packets, policy reports, and instructions doctor reports")
	}
}

func containsAnyString(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertAgentSchemaVersion(t *testing.T, data []byte) {
	t.Helper()
	var report map[string]any
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("parse golden report: %v", err)
	}
	if report["schema_version"] != agentSchemaVersion {
		t.Fatalf("schema_version = %v, want %s", report["schema_version"], agentSchemaVersion)
	}
}

func validateAgentJSONSchema(t *testing.T, data []byte) {
	t.Helper()
	schemaPath, err := filepath.Abs(filepath.Join("..", "..", "schema", "readyci.verification.v0alpha1.schema.json"))
	if err != nil {
		t.Fatalf("resolve agent schema path: %v", err)
	}
	schemaURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(schemaPath)}).String()
	schema, err := jsonschema.Compile(schemaURL)
	if err != nil {
		t.Fatalf("compile agent JSON schema: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var report any
	if err := decoder.Decode(&report); err != nil {
		t.Fatalf("parse golden report for schema validation: %v", err)
	}
	if err := schema.Validate(report); err != nil {
		t.Fatalf("golden report does not validate against %s: %v", schemaURL, err)
	}
}

func agentContractPlanReport() agentPlanReport {
	return agentPlanReport{
		SchemaVersion: agentSchemaVersion,
		Status:        "ready",
		Mode:          "affected_rules",
		Intent:        agentIntentPrePR,
		Goal:          "prepare PR",
		Reason:        "matched_changed_paths",
		ProjectID:     "proj_dev",
		ChangeMode:    "working",
		Checks: []agentAffectedCheck{
			{
				Check:        "web",
				Selected:     true,
				NetworkMode:  "none",
				MatchedPaths: []string{"web/src/pages/Login.tsx"},
				Reasons:      []string{"affected.rules[1] matched web/src/pages/Login.tsx with web/src/**"},
			},
			{
				Check:        "web-e2e",
				Selected:     true,
				NetworkMode:  "none",
				MatchedPaths: []string{"web/src/pages/Login.tsx"},
				Reasons:      []string{"agent intent pre-pr selected because web/src/pages/Login.tsx changed"},
			},
		},
		Policy:        agentContractPolicy(),
		Warnings:      []agentWarning{{Section: "affected.profiles", Message: "profile expanded to browser checks"}},
		VerifyCommand: "readyci verify --json --progress=status --project proj_dev --intent pre-pr --changes working",
	}
}

func agentContractVerifyReport() agentVerifyReport {
	exitCode := 1
	runGroupID := "group_123"
	groupStatus := preflightapi.StatusFailed
	return agentVerifyReport{
		SchemaVersion:  agentSchemaVersion,
		Status:         preflightapi.StatusFailed,
		Mode:           "affected_rules",
		Intent:         agentIntentPrePR,
		Goal:           "prepare PR",
		Reason:         "matched_changed_paths",
		RunGroupID:     &runGroupID,
		GroupStatus:    &groupStatus,
		GroupKind:      "affected_rules",
		RunID:          "run_web",
		Check:          "web",
		Terminal:       true,
		ExitCode:       &exitCode,
		CLIExitCode:    cliExitRemoteFailure,
		Classification: preflightapi.ClassificationTestFailed,
		Retryable:      false,
		AgentAction:    preflightapi.AgentActionFixCode,
		Summary:        "Ready CI check web failed.",
		Checks: []agentAffectedCheck{{
			Check:        "web",
			Selected:     true,
			NetworkMode:  "none",
			MatchedPaths: []string{"web/src/pages/Login.tsx"},
		}},
		Runs: []agentVerifyRunReport{{
			RunID:          "run_web",
			RunGroupID:     "group_123",
			Phase:          "verify",
			Status:         preflightapi.StatusFailed,
			Check:          "web",
			Terminal:       true,
			ExitCode:       &exitCode,
			Classification: preflightapi.ClassificationTestFailed,
			Retryable:      false,
			AgentAction:    preflightapi.AgentActionFixCode,
			Summary:        "Tests failed.",
			LogsCommand:    "readyci runs logs --tail 200 --include-lifecycle --json run_web",
			ExplainCommand: "readyci agent explain --json run_web",
			IdempotencyKey: "task-123:check=web",
			Evidence: []agentEvidence{{
				Stream: "stderr",
				Seq:    1,
				Line:   42,
				Path:   "web/src/App.test.tsx",
				Text:   "expected button to be visible",
			}},
		}},
		Policy: agentContractPolicy(),
		NextActions: []agentNextAction{{
			Kind:    "explain",
			Command: "readyci agent explain --json run_web",
			Reason:  "Inspect the compact repair packet for the failing run.",
		}},
		LogsCommand:    "readyci runs logs --tail 200 --include-lifecycle --json run_web",
		ExplainCommand: "readyci agent explain --json run_web",
	}
}

func agentContractResumeReport() agentVerifyReport {
	exitCode := 0
	return agentVerifyReport{
		SchemaVersion:  agentSchemaVersion,
		Status:         preflightapi.StatusSucceeded,
		RunID:          "run_pass",
		Check:          "web",
		Terminal:       true,
		ExitCode:       &exitCode,
		CLIExitCode:    cliExitSuccess,
		Classification: preflightapi.ClassificationSuccess,
		Retryable:      false,
		AgentAction:    preflightapi.AgentActionNone,
		Summary:        "Ready CI check web passed.",
		Checks:         []agentAffectedCheck{},
		Runs: []agentVerifyRunReport{{
			RunID:          "run_pass",
			Status:         preflightapi.StatusSucceeded,
			Check:          "web",
			Terminal:       true,
			ExitCode:       &exitCode,
			Classification: preflightapi.ClassificationSuccess,
			Retryable:      false,
			AgentAction:    preflightapi.AgentActionNone,
			Summary:        "Ready CI check web passed.",
			LogsCommand:    "readyci runs logs --tail 200 --include-lifecycle --json run_pass",
			ExplainCommand: "readyci agent explain --json run_pass",
		}},
	}
}

func agentContractSkippedReport() agentVerifyReport {
	return agentVerifyReport{
		SchemaVersion:  agentSchemaVersion,
		Status:         "skipped",
		Mode:           "affected_rules",
		Intent:         agentIntentEditLoop,
		Reason:         "no_affected_rules_matched",
		GroupKind:      "affected_rules",
		Terminal:       true,
		CLIExitCode:    cliExitSuccess,
		Classification: preflightapi.ClassificationSuccess,
		Retryable:      false,
		AgentAction:    preflightapi.AgentActionNone,
		Summary:        "No affected rules matched changed files.",
		Checks:         []agentAffectedCheck{},
		Runs:           []agentVerifyRunReport{},
		Policy: &agentPolicyReport{
			NetworkMode:          "none",
			NetworkAllowlist:     []string{},
			SecretsRequested:     false,
			SecretsAuthorized:    false,
			TokenScopesRequired:  []string{},
			TokenScopesMissing:   []string{},
			UploadPreviewCommand: "readyci sync --dry-run --json --changes working",
		},
	}
}

func agentContractInstructionsDoctorReport() agentInstructionsDoctorReport {
	return agentInstructionsDoctorReport{
		SchemaVersion: agentSchemaVersion,
		Status:        "ok",
		Checks: []agentInstructionsDoctorCheck{
			{Name: "instructions_file", Status: "ok", Path: ".agents/skills/readyci-verify/SKILL.md", Message: "file is installed"},
			{Name: "token_values_absent", Status: "ok", Path: ".agents/skills/readyci-verify/SKILL.md", Message: "no Ready CI token values detected"},
			{Name: "reporting_fields", Status: "ok", Message: "instructions require check/run_id/status/classification/exit code reporting"},
			{Name: "readyci_config", Status: "ok", Path: ".readyci.yml", Message: "Ready CI config validates"},
			{Name: "affected_rules", Status: "warn", Path: ".readyci.yml", Message: "no affected rules or profiles configured; agents may use fallback check selection"},
		},
	}
}

func agentContractExplainReport() agentExplainReport {
	exitCode := 1
	evidence := []agentEvidence{{
		Stream: "stderr",
		Seq:    1,
		Line:   42,
		Path:   "web/src/App.test.tsx",
		Text:   "expected button to be visible",
	}}
	evidencePacks := []agentEvidencePack{{
		Name:           "playwright report: playwright-report/**",
		Kind:           "playwright_report",
		Path:           "playwright-report/**",
		Source:         "artifact_path",
		ArtifactID:     "artifact_web",
		SizeBytes:      512,
		PreviewCommand: "readyci agent artifacts plan --json run_web --paths 'playwright-report/**'",
		Description:    "Playwright HTML report with browser journey diagnostics.",
	}}
	return agentExplainReport{
		SchemaVersion: agentSchemaVersion,
		RunID:         "run_web",
		Status:        preflightapi.StatusFailed,
		Terminal:      true,
		ExitCode:      &exitCode,
		Check:         "web",
		Command:       "pnpm test -- --runInBand",
		Diagnosis: agentDiagnosis{
			Kind:        preflightapi.ClassificationTestFailed,
			Confidence:  0.82,
			Summary:     "React test failed in web/src/App.test.tsx.",
			Retryable:   false,
			Action:      preflightapi.AgentActionFixCode,
			PrimaryFile: "web/src/App.test.tsx",
		},
		Repair: agentRepairPacket{
			Classification: preflightapi.ClassificationTestFailed,
			AgentAction:    preflightapi.AgentActionFixCode,
			Summary:        "React test failed in web/src/App.test.tsx.",
			RootCause:      "The command reached the test runner and at least one test failed.",
			PrimaryFile:    "web/src/App.test.tsx",
			TargetFiles: []agentRepairTarget{{
				Path:   "web/src/App.test.tsx",
				Reason: "primary file from failure analysis",
			}},
			FailingCommand: "pnpm test -- --runInBand",
			FailingTest:    "renders login button",
			Evidence:       evidence,
			EvidencePacks:  evidencePacks,
			Commands: []agentRepairCommand{
				{Kind: "inspect_logs", Command: "readyci runs logs --tail 200 --include-lifecycle --json run_web", Reason: "Read full logs if the supplied failure evidence is insufficient for repair."},
				{Kind: "inspect_evidence_pack", Command: "readyci agent artifacts plan --json run_web --paths 'playwright-report/**'", Reason: "Preview playwright_report evidence retained by the run."},
				{Kind: "rerun", Command: "readyci verify --json --progress=status --check web", Reason: "Rerun the same verification after applying the repair."},
			},
			RerunCommand:   "readyci verify --json --progress=status --check web",
			LogsCommand:    "readyci runs logs --tail 200 --include-lifecycle --json run_web",
			PolicyReasons:  []string{"network_mode none enforced by project policy"},
			Confidence:     0.82,
			ConfidenceNote: "medium: classification came from status and log heuristics",
		},
		Evidence:            evidence,
		EvidencePacks:       evidencePacks,
		RecommendedNextStep: "Edit the failing test or code and rerun web.",
		RerunCommand:        "readyci verify --json --progress=status --check web",
		LogsCommand:         "readyci runs logs --tail 200 --include-lifecycle --json run_web",
		PolicyReasons:       []string{"network_mode none enforced by project policy"},
		LogTruncated:        false,
		Spec: preflightapi.RunSpec{
			RunID:          "run_web",
			ProjectID:      "proj_dev",
			Region:         "iad1",
			Image:          "ubuntu-24.04-base",
			Command:        "pnpm test -- --runInBand",
			CommandAlias:   "web",
			RunnerSize:     "standard",
			TimeoutSeconds: 600,
			CacheMode:      "cache-only",
			ArtifactPaths:  []string{"playwright-report/**"},
			NetworkMode:    "none",
			Docker:         false,
			Git:            preflightapi.GitInfo{Dirty: true},
		},
	}
}

func agentContractArtifactPlanReport() agentArtifactHandoffReport {
	createdAt := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	return agentArtifactPlanReport(agentArtifactHandoffOptions{
		RunID: "run_fixture",
		Paths: []string{"test-results/**", "playwright-report/**"},
	}, []preflightapi.ArtifactSummary{{
		ID:            "artifact_fixture",
		RunID:         "run_fixture",
		Name:          "readyci-artifacts",
		Path:          "readyci-artifacts.tar.gz",
		SizeBytes:     64,
		ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAt:     createdAt,
		ExpiresAt:     expiresAt,
	}}, artifactApplyResult{
		RunID:        "run_fixture",
		DryRun:       true,
		AllowEmpty:   true,
		MatchedCount: 2,
		Files: []artifactbundle.File{
			{Path: "test-results/login/trace.zip", SizeBytes: 512},
			{Path: "playwright-report/index.html", SizeBytes: 256},
		},
	})
}

func agentContractArtifactApplyReport() agentArtifactHandoffReport {
	return agentArtifactApplyReport(agentArtifactHandoffOptions{
		RunID: "run_fixture",
		Paths: []string{"test-results/**", "playwright-report/**"},
	}, artifactApplyResult{
		RunID:        "run_fixture",
		DryRun:       true,
		MatchedCount: 2,
		Files: []artifactbundle.File{
			{Path: "test-results/login/trace.zip", SizeBytes: 512},
			{Path: "playwright-report/index.html", SizeBytes: 256},
		},
	})
}

func agentContractPolicy() *agentPolicyReport {
	return &agentPolicyReport{
		NetworkMode:          "none",
		NetworkAllowlist:     []string{},
		SecretsRequested:     false,
		SecretsAuthorized:    false,
		TokenScopesRequired:  append([]string(nil), agentVerifyOnlyScopes...),
		TokenScopesMissing:   []string{},
		UploadPreviewCommand: "readyci sync --dry-run --json --changes working",
	}
}
