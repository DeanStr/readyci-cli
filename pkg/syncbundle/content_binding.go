package syncbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ContentBinding is source metadata that changes the meaning of otherwise
// identical file bytes. It is part of every version-3 content identity.
type ContentBinding struct {
	Submodules       []Submodule       `json:"submodules,omitempty"`
	ReviewedFixtures []ReviewedFixture `json:"reviewed_fixtures,omitempty"`
}

func (manifest Manifest) ContentBinding() ContentBinding {
	return ContentBinding{
		Submodules:       manifest.Submodules,
		ReviewedFixtures: manifest.ReviewedFixtures,
	}
}

func bindContentDigest(content, scope string, binding ContentBinding) (string, error) {
	if err := validateSubmodules(Manifest{Submodules: binding.Submodules}); err != nil {
		return "", err
	}
	fixtures, err := NormalizeReviewedFixtures(binding.ReviewedFixtures)
	if err != nil {
		return "", err
	}
	binding.ReviewedFixtures = fixtures
	metadata, err := json.Marshal(binding)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "readyci-source-v3\x00%s\x00%s\x00", scope, content)
	_, _ = hash.Write(metadata)
	return hex.EncodeToString(hash.Sum(nil)), nil
}
