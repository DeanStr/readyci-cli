package preflightapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// PrivacyExportDigest returns the canonical digest of an export package. The
// digest field is excluded from its own digest by clearing it before encoding.
func PrivacyExportDigest(export AdminPrivacyExport) (string, error) {
	export.Digest = ""
	encoded, err := json.Marshal(export)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ValidatePrivacyExportDigest verifies both the canonical digest syntax and
// the digest's binding to the complete decoded export package.
func ValidatePrivacyExportDigest(export AdminPrivacyExport) error {
	digest := strings.TrimSpace(export.Digest)
	if len(digest) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(digest, "sha256:") || digest != export.Digest {
		return fmt.Errorf("digest must match sha256:<64 lowercase hex characters>")
	}
	encodedDigest, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	if err != nil || hex.EncodeToString(encodedDigest) != strings.TrimPrefix(digest, "sha256:") {
		return fmt.Errorf("digest must match sha256:<64 lowercase hex characters>")
	}
	expected, err := PrivacyExportDigest(export)
	if err != nil {
		return fmt.Errorf("compute privacy export digest: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(digest), []byte(expected)) != 1 {
		return fmt.Errorf("digest does not match privacy export contents")
	}
	return nil
}
