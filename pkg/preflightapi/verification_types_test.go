package preflightapi

import (
	"strings"
	"testing"
)

func validVerificationRequest() CreateVerificationSessionRequest {
	return CreateVerificationSessionRequest{
		ProjectID: "project", IdempotencyKey: "task-1",
		Recipe:   VerificationRecipe{SchemaVersion: VerificationRecipeSchemaVersion, Rerunnable: true, ChangeMode: "working", Source: &VerificationSourceRecipe{IncludeUntracked: true, UseProjectIgnore: true}},
		Snapshot: VerificationSnapshot{Digest: "sha256:" + strings.Repeat("a", 64), ChangeMode: "working", ChangedFiles: []string{"a.go"}},
		Coverage: VerificationCoverage{MatchedFiles: []string{"a.go"}},
		Plan:     VerificationPlan{Nodes: []VerificationNodePlan{{ID: "test", Name: "test", Kind: "check", Required: true, Retry: VerificationRetryPolicy{MaxAttempts: 2}, Run: CreateRunRequest{Command: "go test ./..."}}}},
	}
}

func TestNormalizeCreateVerificationSessionRequestRequiresRecipe(t *testing.T) {
	req := validVerificationRequest()
	req.Recipe = VerificationRecipe{}
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil || !strings.Contains(err.Error(), "recipe.schema_version is required") {
		t.Fatalf("missing recipe error = %v", err)
	}
	req.Recipe.SchemaVersion = VerificationRecipeSchemaVersion
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil || !strings.Contains(err.Error(), "recipe.change_mode is required") {
		t.Fatalf("missing recipe change mode error = %v", err)
	}
	req = validVerificationRequest()
	req.Recipe.Source = nil
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil || !strings.Contains(err.Error(), "recipe.source is required") {
		t.Fatalf("missing recipe source error = %v", err)
	}
}

func TestNormalizeCreateVerificationSessionRequestRejectsRecipeSnapshotModeMismatch(t *testing.T) {
	req := validVerificationRequest()
	req.Recipe.ChangeMode = "staged"
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil || !strings.Contains(err.Error(), "does not match snapshot") {
		t.Fatalf("recipe mismatch error = %v", err)
	}
}

func TestNormalizeCreateVerificationSessionRequestSealsCanonicalPlan(t *testing.T) {
	req, err := NormalizeCreateVerificationSessionRequest(validVerificationRequest())
	if err != nil {
		t.Fatal(err)
	}
	if req.SchemaVersion != VerificationSchemaVersion || !strings.HasPrefix(req.Plan.Digest, "sha256:") {
		t.Fatalf("normalized request = %+v", req)
	}
	if req.Plan.Nodes[0].Run.ProjectID != "project" {
		t.Fatalf("node project = %q", req.Plan.Nodes[0].Run.ProjectID)
	}
}

func TestNormalizeCreateVerificationSessionRequestPersistsVersionedRecipe(t *testing.T) {
	req := validVerificationRequest()
	req.Recipe = VerificationRecipe{
		SchemaVersion:  VerificationRecipeSchemaVersion,
		Check:          " cli ",
		Rerunnable:     true,
		ChangeMode:     "working",
		RequestSecrets: true,
		RetryCount:     2,
		RetryProvided:  true,
		Source: &VerificationSourceRecipe{
			ExcludePatterns:        []string{" tmp/** ", "dist/**", "tmp/**"},
			DefaultExcludePatterns: []string{".env*"},
			IncludeUntracked:       true,
			UseProjectIgnore:       true,
		},
	}
	normalized, err := NormalizeCreateVerificationSessionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Recipe.SchemaVersion != VerificationRecipeSchemaVersion ||
		normalized.Recipe.Check != "cli" || normalized.Recipe.RetryCount != 2 || normalized.Recipe.Digest == "" ||
		strings.Join(normalized.Recipe.Source.ExcludePatterns, ",") != "dist/**,tmp/**" {
		t.Fatalf("recipe = %+v", normalized.Recipe)
	}
	req.Recipe.SchemaVersion = "readyci.verification_recipe.v2"
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil {
		t.Fatal("unsupported recipe schema was accepted")
	}
}

func TestNormalizeCreateVerificationSessionRequestDefaultsDependencyConditionToOnSuccess(t *testing.T) {
	req := validVerificationRequest()
	req.Plan.Nodes = append(req.Plan.Nodes, VerificationNodePlan{
		ID:        "dependent",
		Name:      "dependent",
		Kind:      "check",
		Required:  true,
		DependsOn: []string{"test"},
		Run:       CreateRunRequest{Command: "go vet ./..."},
	})

	normalized, err := NormalizeCreateVerificationSessionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if got := normalized.Plan.Nodes[1].Condition; got != VerificationNodeConditionOnSuccess {
		t.Fatalf("dependent node condition = %q, want %q", got, VerificationNodeConditionOnSuccess)
	}
}

func TestNormalizeCreateVerificationSessionRequestRejectsCoverageGapAndCycle(t *testing.T) {
	req := validVerificationRequest()
	req.Coverage = VerificationCoverage{}
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil || !strings.Contains(err.Error(), "missing from coverage") {
		t.Fatalf("coverage error = %v", err)
	}
	req = validVerificationRequest()
	req.Plan.Nodes[0].DependsOn = []string{"test"}
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil || !strings.Contains(err.Error(), "depend on itself") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestNormalizeCreateVerificationSessionRequestRequiresHarnessGapDeclaration(t *testing.T) {
	req := validVerificationRequest()
	req.Plan.Nodes = nil
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil || !strings.Contains(err.Error(), "reported as uncovered") {
		t.Fatalf("missing harness gap error = %v", err)
	}
	req.Coverage = VerificationCoverage{UncoveredFiles: []string{"a.go"}}
	if _, err := NormalizeCreateVerificationSessionRequest(req); err != nil {
		t.Fatalf("declared harness gap: %v", err)
	}
}

func TestNormalizeCreateVerificationSessionRequestRejectsUnsafeChangedPath(t *testing.T) {
	for _, unsafe := range []string{"../outside", ".git/config", "nested/.GIT/hooks/pre-commit", "/absolute"} {
		req := validVerificationRequest()
		req.Snapshot.ChangedFiles = []string{unsafe}
		req.Coverage.MatchedFiles = []string{unsafe}
		if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil {
			t.Fatalf("accepted unsafe changed path %q", unsafe)
		}
	}
}

func TestNormalizeCreateVerificationSessionRequestBoundsPublicMetadata(t *testing.T) {
	req := validVerificationRequest()
	req.Goal = "unsafe\nobjective"
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil || !strings.Contains(err.Error(), "control") {
		t.Fatalf("control-character error = %v", err)
	}
	req = validVerificationRequest()
	req.Plan.Nodes = make([]VerificationNodePlan, 129)
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil || !strings.Contains(err.Error(), "128 nodes") {
		t.Fatalf("node limit error = %v", err)
	}
}

func TestExecutionPurposeIsSealedAndCannotClaimAuthority(t *testing.T) {
	req := validVerificationRequest()
	req.Recipe.Purpose = ExecutionPurpose
	normalized, err := NormalizeCreateVerificationSessionRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	normalized.Recipe.Purpose = ""
	if _, err := NormalizeCreateVerificationSessionRequest(normalized); err == nil {
		t.Fatal("purpose changed without invalidating recipe digest")
	}
	req.Plan.Authority = VerificationPlanAuthorityServer
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil {
		t.Fatal("execution claimed verification authority")
	}
	req.Plan.Authority = ""
	req.Plan.Nodes = nil
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil {
		t.Fatal("empty execution accepted")
	}
	req = validVerificationRequest()
	req.Recipe.Source.UploadBaselineSessionID = "other-job"
	if _, err := NormalizeCreateVerificationSessionRequest(req); err == nil {
		t.Fatal("verification accepted execution baseline hint")
	}
}
