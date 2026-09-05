package preflightapi

import (
	"strings"
	"testing"
)

func TestNormalizeEvidenceManifest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	manifest, err := NormalizeEvidenceManifest(EvidenceManifest{
		Producer:  EvidenceProducer{Name: "go-test-json"},
		Execution: EvidenceExecution{WorkspaceDigest: digest, Attempt: 1},
		Claims:    []EvidenceClaim{{Criterion: "unit-tests", Outcome: "passed", TrustLevel: EvidenceTrustUnchangedTest}},
		Files:     []EvidenceFile{{Path: "test-results/results.json", Kind: "test_results", SHA256: digest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != EvidenceSchemaVersion {
		t.Fatalf("schema = %q", manifest.SchemaVersion)
	}
}

func TestNormalizeEvidenceManifestRejectsUntrustedShape(t *testing.T) {
	digest := "sha256:" + strings.Repeat("b", 64)
	_, err := NormalizeEvidenceManifest(EvidenceManifest{Producer: EvidenceProducer{Name: "adapter"}, Execution: EvidenceExecution{WorkspaceDigest: digest, Attempt: 1}, Claims: []EvidenceClaim{{Criterion: "tests", Outcome: "passed", TrustLevel: "trusted"}}})
	if err == nil {
		t.Fatal("accepted invalid trust level")
	}
}

func TestNormalizeEvidenceManifestRequiresHashedProvenanceAndLowConfidenceInference(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	manifest := EvidenceManifest{Producer: EvidenceProducer{Name: "adapter"}, Execution: EvidenceExecution{WorkspaceDigest: digest, TestDigest: "not-a-digest", Attempt: 1}}
	if _, err := NormalizeEvidenceManifest(manifest); err == nil || !strings.Contains(err.Error(), "test_digest") {
		t.Fatalf("test digest error = %v", err)
	}
	manifest.Execution.TestDigest = digest
	manifest.Claims = []EvidenceClaim{{Criterion: "tests", Outcome: "passed", TrustLevel: EvidenceTrustHeuristic, Source: "inferred", Confidence: "high"}}
	if _, err := NormalizeEvidenceManifest(manifest); err == nil || !strings.Contains(err.Error(), "low confidence") {
		t.Fatalf("inference confidence error = %v", err)
	}
}

func TestNormalizeStructuredTestResult(t *testing.T) {
	result, err := NormalizeStructuredTestResult(StructuredTestResult{Adapter: "go-test-json", Test: "TestLogin", Status: "failed", File: "pkg/auth/login_test.go", Line: 42, Attempt: 1})
	if err != nil || result.File != "pkg/auth/login_test.go" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := NormalizeStructuredTestResult(StructuredTestResult{Adapter: "junit", Test: "escape", Status: "passed", File: "../outside", Attempt: 1}); err == nil {
		t.Fatal("accepted escaping structured test path")
	}
}
