package verificationplan

import (
	"strings"
)

// ShellQuoteArg quotes an argument for the generated shell script, leaving
// already-safe arguments unquoted.
func ShellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if isShellSafeArg(arg) {
		return arg
	}

	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range arg {
		if r == '\'' {
			b.WriteString("'\\''")
			continue
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

func isShellSafeArg(arg string) bool {
	for _, r := range arg {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '_', '@', '%', '+', '=', ':', ',', '.', '/', '-':
			continue
		default:
			return false
		}
	}
	return true
}
