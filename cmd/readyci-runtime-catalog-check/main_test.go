package main

import (
	"strings"
	"testing"
	"time"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func TestCatalogRefreshDeadline(t *testing.T) {
	reviewedAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	validBefore := reviewedAt.Add(72 * time.Hour)
	for _, test := range []struct {
		name      string
		now       time.Time
		window    time.Duration
		wantError bool
	}{
		{"reviewed", reviewedAt, 48 * time.Hour, false},
		{"before refresh deadline", validBefore.Add(-48*time.Hour - time.Second), 48 * time.Hour, false},
		{"refresh deadline", validBefore.Add(-48 * time.Hour), 48 * time.Hour, true},
		{"expired", validBefore, 0, true},
		{"not yet reviewed", reviewedAt.Add(-time.Second), 0, true},
		{"negative window", reviewedAt, -time.Hour, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := checkCatalogWindow(test.now, test.window, reviewedAt, validBefore); (err != nil) != test.wantError {
				t.Fatalf("checkCatalog: %v; want error %v", err, test.wantError)
			}
		})
	}
}

func TestFreshReviewDoesNotExtendTransitionDeadline(t *testing.T) {
	reviewedAt := time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC)
	validBefore := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	if err := checkCatalogWindow(reviewedAt, 0, reviewedAt, validBefore); err != nil {
		t.Fatalf("review inside valid import interval rejected: %v", err)
	}
	if err := checkCatalogWindow(reviewedAt, 48*time.Hour, reviewedAt, validBefore); err == nil || !strings.Contains(err.Error(), "within 48h") {
		t.Fatalf("fresh review hid insufficient publication lead time: %v", err)
	}
	if err := checkCatalogWindow(reviewedAt, 0, reviewedAt, reviewedAt); err == nil {
		t.Fatal("empty catalog interval accepted")
	}
}

func TestCatalogCheckUsesReviewedSnapshotWindow(t *testing.T) {
	catalog := preflightapi.NodeRuntimeAliasCatalogMetadata()
	if err := checkCatalog(catalog.ReviewedAt, 0); err != nil {
		t.Fatalf("active reviewed catalog rejected: %v", err)
	}
	remaining := catalog.ValidBefore.Sub(catalog.ReviewedAt)
	if err := checkCatalog(catalog.ReviewedAt, remaining-time.Nanosecond); err != nil {
		t.Fatalf("valid remaining interval rejected: %v", err)
	}
	if err := checkCatalog(catalog.ReviewedAt, remaining); err == nil {
		t.Fatal("exact expiry deadline accepted")
	}
	if err := checkCatalog(catalog.ValidBefore, 0); err == nil {
		t.Fatal("expired reviewed catalog accepted")
	}
}
