package main

import (
	"net/url"
	"strings"
)

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
