package main

import "testing"

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		value string
		want  int64
	}{
		{value: "", want: 0},
		{value: "42", want: 42},
		{value: "30GiB", want: 30 * 1024 * 1024 * 1024},
		{value: "1024 MiB", want: 1024 * 1024 * 1024},
		{value: "30GB", want: 30 * 1000 * 1000 * 1000},
	}
	for _, test := range tests {
		got, err := parseByteSize(test.value)
		if err != nil {
			t.Fatalf("parseByteSize(%q): %v", test.value, err)
		}
		if got != test.want {
			t.Fatalf("parseByteSize(%q) = %d, want %d", test.value, got, test.want)
		}
	}
}

func TestParseByteSizeRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"-1GiB", "1.5GiB", "10XB"} {
		if _, err := parseByteSize(value); err == nil {
			t.Fatalf("parseByteSize(%q) succeeded, want error", value)
		}
	}
}
