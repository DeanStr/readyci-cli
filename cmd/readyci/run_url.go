package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

func openRunURL(dashboardURL, runID string) {
	runURL := runDashboardURL(dashboardURL, runID)
	if runURL == "" {
		fmt.Fprintf(os.Stderr, "readyci: run %s created; set READYCI_DASHBOARD_URL to use --open\n", runID)
		return
	}
	if err := openBrowser(runURL); err != nil {
		fmt.Fprintf(os.Stderr, "readyci: open %s\n", runURL)
		return
	}
	fmt.Fprintf(os.Stderr, "readyci: opened %s\n", runURL)
}

func runDashboardURL(dashboardURL, runID string) string {
	dashboardURL = normalizeDashboardURLBase(dashboardURL)
	if dashboardURL == "" {
		return ""
	}
	return dashboardURL + "/ops/runs?run=" + url.QueryEscape(runID)
}

func normalizeDashboardURLBase(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		return strings.TrimRight(parsed.String(), "/")
	}
	return strings.TrimRight(value, "/")
}
