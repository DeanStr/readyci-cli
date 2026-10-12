package main

import (
	"regexp"
	"strings"
)

// These are the unfinished forms of the patterns in cliSecretRedactor. Keep
// candidate credentials until their delimiter arrives, rather than exposing a
// suffix when the next event continues a value already masked in this event.
var unfinishedLogCredentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer(?:\s+[^\s,;]*)?$`),
	regexp.MustCompile(`https?://[^/@\s]*$`),
	regexp.MustCompile(`(?i)[?&](?:token|api[_-]?key|access[_-]?token|secret|signature|x-amz-signature|x-amz-credential|x-amz-security-token)=[^&#\s]*$`),
}

var logCredentialPrefixes = func() []string {
	prefixes := []string{"bearer", "http://", "https://"}
	for _, name := range []string{"token", "apikey", "api_key", "api-key", "accesstoken", "access_token", "access-token", "secret", "signature", "x-amz-signature", "x-amz-credential", "x-amz-security-token"} {
		prefixes = append(prefixes, "?"+name+"=", "&"+name+"=")
	}
	return prefixes
}()

type executionLogRedactor struct {
	redactor cliSecretRedactor
	pending  string
}

func (r *executionLogRedactor) redact(data string, final bool) string {
	buffer := r.pending + data
	end := len(buffer)
	if !final {
		for _, pattern := range unfinishedLogCredentialPatterns {
			if match := pattern.FindStringIndex(buffer); match != nil && match[0] < end {
				end = match[0]
			}
		}
		for _, prefix := range logCredentialPrefixes {
			end = min(end, partialLogCredentialStart(buffer, prefix, true))
		}
		for _, secret := range r.redactor.exact {
			end = min(end, partialLogCredentialStart(buffer, secret, false))
		}
		// An incomplete candidate may overlap a complete credential. Never split
		// that complete match while emitting the prefix (including overlapping keys).
		for {
			previous := end
			for _, secret := range r.redactor.exact {
				for start := 0; start < end; {
					offset := strings.Index(buffer[start:], secret)
					if offset < 0 {
						break
					}
					start += offset
					if start < end && start+len(secret) > end {
						end = start
						break
					}
					start++
				}
			}
			for _, pattern := range []*regexp.Regexp{bearerCredentialPattern, urlCredentialPattern, secretQueryPattern} {
				for _, match := range pattern.FindAllStringIndex(buffer, -1) {
					if match[0] < end && match[1] > end {
						end = match[0]
					}
				}
			}
			if end == previous {
				break
			}
		}
	}
	output := r.redactor.Redact(buffer[:end])
	r.pending = strings.Clone(buffer[end:])
	return output
}

func partialLogCredentialStart(buffer, prefix string, fold bool) int {
	for count := min(len(buffer), len(prefix)-1); count > 0; count-- {
		suffix := buffer[len(buffer)-count:]
		if suffix == prefix[:count] || (fold && strings.EqualFold(suffix, prefix[:count])) {
			return len(buffer) - count
		}
	}
	return len(buffer)
}
