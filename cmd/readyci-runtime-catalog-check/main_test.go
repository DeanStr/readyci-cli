package main

import (
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestCatalogRefreshDeadline(t *testing.T) {
	catalog := preflightapi.NodeRuntimeAliasCatalogMetadata()
	for _, test := range []struct {
		name      string
		now       time.Time
		window    time.Duration
		wantError bool
	}{
		{"reviewed", catalog.ReviewedAt, 48 * time.Hour, false},
		{"before refresh deadline", catalog.ValidBefore.Add(-48*time.Hour - time.Second), 48 * time.Hour, false},
		{"refresh deadline", catalog.ValidBefore.Add(-48 * time.Hour), 48 * time.Hour, true},
		{"expired", catalog.ValidBefore, 0, true},
		{"not yet reviewed", catalog.ReviewedAt.Add(-time.Second), 0, true},
		{"negative window", catalog.ReviewedAt, -time.Hour, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := checkCatalog(test.now, test.window); (err != nil) != test.wantError {
				t.Fatalf("checkCatalog: %v; want error %v", err, test.wantError)
			}
		})
	}
}
