package textlimit

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPrefixUTF8WithinBytesPreservesValidEncoding(t *testing.T) {
	got := PrefixUTF8WithinBytes("ab界cd", 5)
	if got != "ab界" {
		t.Fatalf("prefix = %q, want ab界", got)
	}
	if !utf8.ValidString(got) || len(got) > 5 {
		t.Fatalf("prefix = %q len=%d, want valid UTF-8 within limit", got, len(got))
	}
}

func TestPrefixUTF8WithinBytesReplacesInvalidBytesWithoutOverTruncating(t *testing.T) {
	value := "a" + string([]byte{0xff}) + strings.Repeat("b", 20)
	got := PrefixUTF8WithinBytes(value, 8)
	if !utf8.ValidString(got) || len(got) > 8 {
		t.Fatalf("prefix = %q len=%d, want valid UTF-8 within limit", got, len(got))
	}
	if !strings.Contains(got, replacement) || !strings.Contains(got, "bbb") {
		t.Fatalf("prefix = %q, want replacement and bytes after invalid input", got)
	}
}

func TestSuffixUTF8WithinBytesPreservesValidEncoding(t *testing.T) {
	got := SuffixUTF8WithinBytes("ab界cd", 5)
	if got != "界cd" {
		t.Fatalf("suffix = %q, want 界cd", got)
	}
	if !utf8.ValidString(got) || len(got) > 5 {
		t.Fatalf("suffix = %q len=%d, want valid UTF-8 within limit", got, len(got))
	}
}

func TestTruncateUTF8BytesWithSuffixPreservesLimit(t *testing.T) {
	got := TruncateUTF8BytesWithSuffix(strings.Repeat("界", 20), 14, "...")
	if !utf8.ValidString(got) || len(got) > 14 {
		t.Fatalf("truncated = %q len=%d, want valid UTF-8 within limit", got, len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated = %q, want suffix", got)
	}
}
