package diagnosiscommon

import "regexp"

var (
	networkEndpointLocation = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://[^\s"'<>]+|(?:--(?:resolve|connect-to)(?:=|\s+)|(?:connect(?:ion)?(?:\s+fail(?:ed)?)?\s+(?:to|for)|dial\s+(?:tcp|udp)[46]?)\s+)["']?(?:[a-z0-9_.-]+|\[[^\]\s]+\]):[0-9]+[^\s"'<>]*)`)
	socketFailureDiagnostic = regexp.MustCompile(`(?i)\b(?:connect|connection|bind|socket)\b[^\r\n]*(?:cannot assign requested address|connection refused|no route to host)`)
)

// FileLocationSearchText removes terminal color and socket/URL endpoint spans
// before a caller searches file:line locations. A filename such as example.com
// remains valid outside endpoint contexts; host:port is not a source location.
func FileLocationSearchText(text string) string {
	return networkEndpointLocation.ReplaceAllString(StripDiagnosticANSI(text), " ")
}

// SocketFailureDiagnostic recognizes an emitted socket operation and its
// concrete error. It identifies useful evidence without assigning blame or a
// hosted classification to the network/runner/repository.
func SocketFailureDiagnostic(text string) bool {
	return socketFailureDiagnostic.MatchString(StripDiagnosticANSI(text))
}
