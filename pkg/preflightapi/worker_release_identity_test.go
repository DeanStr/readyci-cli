package preflightapi

import (
	"strings"
	"testing"
)

func TestWorkerReleaseIdentityBindsEveryComponent(t *testing.T) {
	identity, err := NewWorkerReleaseIdentity(
		strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64),
		strings.Repeat("4", 64), strings.Repeat("5", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkerReleaseIdentity(identity); err != nil {
		t.Fatalf("validate identity: %v", err)
	}
	tampered := identity
	tampered.IsolationConfigSHA256 = strings.Repeat("6", 64)
	if err := ValidateWorkerReleaseIdentity(tampered); err == nil {
		t.Fatal("identity with a changed isolation component retained a valid release digest")
	}
}
