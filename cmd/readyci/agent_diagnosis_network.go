package main

import (
	"strings"

	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const opaqueSigstoreInitializationFailure = "no valid Sigstore verifiers could be initialized"
const sigstoreAdvisoryLogLimit = 1 << 20

// This is a prerequisite review advisory, not evidence that network policy
// caused the failure. It leaves the authoritative execution diagnosis intact.
func sigstorePrerequisiteAdvisory(status, classification, networkMode string, events []preflightapi.RunEvent) string {
	if status != preflightapi.StatusFailed || classification != preflightapi.ClassificationTestFailed || (networkMode != "none" && networkMode != "allowlist") {
		return ""
	}
	if !hasOpaqueSigstoreInitializationFailure(events) {
		return ""
	}
	return "Logs report Sigstore verifier initialization failure. Review trust-metadata endpoint access and this run's network policy before editing tests; the available evidence does not establish the cause."
}

func hasOpaqueSigstoreInitializationFailure(events []preflightapi.RunEvent) bool {
	return prerequisiteOutputMarkers(events, false, opaqueSigstoreInitializationFailure)[0]
}

func kubernetesPrerequisiteAdvisories(status string, events []preflightapi.RunEvent) []string {
	if status != preflightapi.StatusFailed && status != preflightapi.StatusTimedOut {
		return nil
	}
	markers := prerequisiteOutputMarkers(events, true,
		"unable to load in-cluster configuration, KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT must be defined",
		"KUBERNETES_SERVER_BIN_DIR must be set",
	)
	var advisories []string
	if markers[0] {
		advisories = append(advisories, "Command logs report missing Kubernetes cluster configuration. Review the selected test scope and its cluster prerequisites. This secondary guidance does not explain or replace the terminal failure.")
	}
	if markers[1] {
		advisories = append(advisories, "Command logs report KUBERNETES_SERVER_BIN_DIR must be set. Review the selected test scope and required Kubernetes server binaries. This secondary guidance does not explain or replace the terminal failure.")
	}
	return advisories
}

func prerequisiteOutputMarkers(events []preflightapi.RunEvent, commandPhaseOnly bool, markers ...string) []bool {
	// Only actual command output establishes the advisory. Keep bounded tails
	// for chunked output without joining different streams, phases, or steps.
	matches := make([]bool, len(markers))
	maxMarkerLength := 0
	for _, marker := range markers {
		maxMarkerLength = max(maxMarkerLength, len(marker))
	}
	if maxMarkerLength == 0 {
		return matches
	}
	type source struct{ stream, phase, step string }
	tails := map[source]string{}
	remaining := sigstoreAdvisoryLogLimit
	for _, event := range events {
		if event.Service != "" || (event.Type != preflightapi.EventStdout && event.Type != preflightapi.EventStderr) || (commandPhaseOnly && event.Phase != "command") {
			continue
		}
		key := source{event.Type, event.Phase, event.Step}
		if _, known := tails[key]; !known && len(tails) >= 16 {
			continue
		}
		if remaining == 0 {
			break
		}
		data, err := event.DataBytes()
		if err != nil {
			// Retain the source count but discard incomplete diagnostic bytes.
			tails[key] = ""
			continue
		}
		if len(data) > remaining {
			data = data[:remaining]
		}
		remaining -= len(data)
		text := tails[key] + string(data)
		complete := true
		for index, marker := range markers {
			matches[index] = matches[index] || strings.Contains(text, marker)
			complete = complete && matches[index]
		}
		if complete {
			return matches
		}
		if len(text) >= maxMarkerLength {
			text = text[len(text)-maxMarkerLength+1:]
		}
		tails[key] = text
	}
	return matches
}
