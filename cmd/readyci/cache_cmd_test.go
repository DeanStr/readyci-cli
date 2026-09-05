package main

import (
	"strings"
	"testing"
)

func TestCacheCommandsRejectUnsafeProjectID(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "list flag", run: func() error { return cacheListCommand([]string{"--project", "owner\nrepo"}) }},
		{name: "list positional", run: func() error { return cacheListCommand([]string{"owner\nrepo"}) }},
		{name: "purge flag", run: func() error { return cachePurgeCommand([]string{"--project", "owner\nrepo"}) }},
		{name: "purge positional", run: func() error { return cachePurgeCommand([]string{"owner\nrepo"}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), "project_id must not contain control characters") {
				t.Fatalf("cache command err = %v, want project_id rejection", err)
			}
		})
	}
}
