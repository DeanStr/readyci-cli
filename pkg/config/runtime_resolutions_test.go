package config

import (
	"strings"
	"testing"
)

func TestConfigRejectsExecutionAliasMetadata(t *testing.T) {
	for _, field := range []string{"runtime_resolutions: {node: {selector: lts/*}}", "<<: {runtime_resolutions: {node: {selector: lts/*}}}"} {
		_, err := ParseProjectConfig([]byte("checks:\n  test:\n    command: node --test\n    runtime_requirements: {node: \"24\"}\n    "+field+"\n"), "alias.readyci.yml")
		if err == nil || !strings.Contains(err.Error(), "runtime_resolutions") {
			t.Fatalf("execution alias metadata was accepted: %v", err)
		}
	}
}
