package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

type executionSourceCache struct {
	SessionID   string `json:"session_id"`
	BaselineKey string `json:"baseline_key"`
}

// This is only a transfer hint. The service checks ownership and reconstructs
// the full snapshot; a local cache entry cannot authorize source or a plan.
func executionSourceCachePath(apiURL, project string) string {
	root, err := inferenceRoot(".")
	if err != nil {
		return ""
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	hash := sha256.Sum256([]byte(normalizeAPIURL(apiURL) + "\x00" + project + "\x00" + root))
	return filepath.Join(dir, "readyci", "sources", hex.EncodeToString(hash[:])+".json")
}

func readExecutionSourceCache(apiURL, project string) executionSourceCache {
	path := executionSourceCachePath(apiURL, project)
	file, err := os.Open(path)
	if err != nil {
		return executionSourceCache{}
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return executionSourceCache{}
	}
	var entry executionSourceCache
	if json.Unmarshal(data, &entry) != nil {
		return executionSourceCache{}
	}
	return entry
}

func rememberExecutionSource(apiURL, project, baseline string, session preflightapi.VerificationSession) {
	if (session.State != preflightapi.VerificationStateTerminal && session.State != preflightapi.VerificationStateSealed) || baseline == "" {
		return
	}
	hasAttempt := false
	for _, node := range session.Nodes {
		hasAttempt = hasAttempt || len(node.Attempts) > 0
	}
	if !hasAttempt {
		return
	}
	path := executionSourceCachePath(apiURL, project)
	if path == "" || os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	data, _ := json.Marshal(executionSourceCache{session.SessionID, baseline})
	file, err := os.CreateTemp(filepath.Dir(path), ".source-*")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr == nil && closeErr == nil {
		_ = os.Rename(file.Name(), path)
	}
}
