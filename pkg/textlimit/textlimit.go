package textlimit

import (
	"strings"
	"unicode/utf8"
)

const replacement = "\uFFFD"

func PrefixUTF8WithinBytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	value = strings.ToValidUTF8(value, replacement)
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

func SuffixUTF8WithinBytes(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	value = strings.ToValidUTF8(value, replacement)
	if len(value) <= maxBytes {
		return value
	}
	start := len(value) - maxBytes
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}

func TruncateUTF8BytesWithSuffix(value string, maxBytes int, suffix string) string {
	if maxBytes <= 0 {
		return ""
	}
	value = strings.ToValidUTF8(value, replacement)
	if len(value) <= maxBytes {
		return value
	}
	suffix = strings.ToValidUTF8(suffix, replacement)
	if len(suffix) >= maxBytes {
		return PrefixUTF8WithinBytes(suffix, maxBytes)
	}
	return PrefixUTF8WithinBytes(value, maxBytes-len(suffix)) + suffix
}
