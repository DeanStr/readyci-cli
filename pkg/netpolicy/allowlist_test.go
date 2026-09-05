package netpolicy

import (
	"net/netip"
	"strings"
	"testing"
)

func TestAllowlistTargetValidation(t *testing.T) {
	tests := []struct {
		name    string
		entry   string
		want    string
		wantErr string
	}{
		{name: "hostname", entry: "API.GitHub.com", want: "api.github.com"},
		{name: "url", entry: "https://example.com/api", want: "example.com"},
		{name: "host port", entry: "example.com:443", want: "example.com"},
		{name: "public IPv4", entry: "93.184.216.34", want: "93.184.216.34"},
		{name: "private IPv4", entry: "10.0.0.1", wantErr: "public IPv4"},
		{name: "documentation IPv4", entry: "192.0.2.1", want: "192.0.2.1"},
		{name: "benchmark IPv4", entry: "198.18.0.1", want: "198.18.0.1"},
		{name: "cidr", entry: "93.184.216.0/24", wantErr: "CIDR"},
		{name: "wildcard", entry: "*.example.com", wantErr: "wildcards"},
		{name: "single label", entry: "localhost", wantErr: "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AllowlistTarget(tt.entry)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("AllowlistTarget(%q) error = %v, want %q", tt.entry, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("AllowlistTarget(%q): %v", tt.entry, err)
			}
			if got != tt.want {
				t.Fatalf("AllowlistTarget(%q) = %q, want %q", tt.entry, got, tt.want)
			}
		})
	}
}

func TestNormalizeAllowlistEntriesDedupesAndSorts(t *testing.T) {
	got, err := NormalizeAllowlistEntries([]string{"API.GitHub.com", "93.184.216.34", "api.github.com"})
	if err != nil {
		t.Fatalf("NormalizeAllowlistEntries: %v", err)
	}
	want := []string{"93.184.216.34", "api.github.com"}
	if len(got) != len(want) {
		t.Fatalf("normalized entries = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalized entries = %#v, want %#v", got, want)
		}
	}
}

func TestNormalizeAllowlistEntriesUsesBasicPublicIPv4Policy(t *testing.T) {
	got, err := NormalizeAllowlistEntries([]string{"100.64.0.1", "192.0.2.1", "198.18.0.1"})
	if err != nil {
		t.Fatalf("NormalizeAllowlistEntries: %v", err)
	}
	want := []string{"100.64.0.1", "192.0.2.1", "198.18.0.1"}
	if len(got) != len(want) {
		t.Fatalf("normalized entries = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalized entries = %#v, want %#v", got, want)
		}
	}
}

func TestNormalizeAllowlistEntriesWithIPValidator(t *testing.T) {
	got, err := NormalizeAllowlistEntriesWithIPValidator([]string{"192.0.2.1"}, func(_ netip.Addr) bool {
		return false
	})
	if err == nil {
		t.Fatalf("NormalizeAllowlistEntriesWithIPValidator = %#v, want error", got)
	}
}

func TestNilIPValidatorUsesBasicPublicIPv4Policy(t *testing.T) {
	if _, err := AllowlistTargetWithIPValidator("10.0.0.1", nil); err == nil || !strings.Contains(err.Error(), "public IPv4") {
		t.Fatalf("AllowlistTargetWithIPValidator nil validator error = %v, want public IPv4 rejection", err)
	}
	if _, err := NormalizeAllowlistEntriesWithIPValidator([]string{"127.0.0.1"}, nil); err == nil || !strings.Contains(err.Error(), "public IPv4") {
		t.Fatalf("NormalizeAllowlistEntriesWithIPValidator nil validator error = %v, want public IPv4 rejection", err)
	}
}

func TestBlockedTCPDestinationPortsReturnsCopy(t *testing.T) {
	got := BlockedTCPDestinationPorts()
	if len(got) == 0 {
		t.Fatal("blocked TCP destination ports should not be empty")
	}
	if !containsInt(got, 25) || !containsInt(got, 465) || !containsInt(got, 587) || !containsInt(got, 2525) {
		t.Fatalf("blocked TCP destination ports = %v, want SMTP submission ports", got)
	}
	original := got[0]
	got[0] = 443
	next := BlockedTCPDestinationPorts()
	if next[0] != original {
		t.Fatalf("blocked TCP destination ports were mutated through returned slice: got %d, want %d", next[0], original)
	}
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
