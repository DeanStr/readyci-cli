package preflightapi

import (
	"strings"
	"testing"
	"time"
)

func TestValidatePrivacyExportDigestBindsCompletePackage(t *testing.T) {
	export := AdminPrivacyExport{
		SchemaVersion: PrivacyExportSchemaVersion,
		CaseID:        "case_digest",
		GeneratedAt:   time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC),
		Subject:       PrivacyExportSubject{UserID: "user_digest", Email: "digest@example.test"},
		Manifest:      map[string]int64{"users": 1},
	}
	var err error
	export.Digest, err = PrivacyExportDigest(export)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivacyExportDigest(export); err != nil {
		t.Fatalf("validate canonical export: %v", err)
	}

	export.Manifest["users"] = 2
	if err := ValidatePrivacyExportDigest(export); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("modified export digest error = %v, want mismatch", err)
	}
}

func TestValidatePrivacyExportDigestRejectsNoncanonicalValues(t *testing.T) {
	for _, digest := range []string{
		"sha256:test",
		"SHA256:" + strings.Repeat("a", 64),
		"sha256:" + strings.Repeat("A", 64),
		" sha256:" + strings.Repeat("a", 64),
	} {
		export := AdminPrivacyExport{Digest: digest}
		if err := ValidatePrivacyExportDigest(export); err == nil || !strings.Contains(err.Error(), "64 lowercase hex") {
			t.Fatalf("digest %q error = %v, want canonical-format failure", digest, err)
		}
	}
}
