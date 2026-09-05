package main

import (
	"net"
	"net/url"
	"strings"
)

func executionProjectID(apiURL, requested string) (string, error) {
	if err := validateControlFreeValue("project_id", requested); err != nil {
		return "", cliConfigError(err.Error(), err)
	}
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested, nil
	}
	if localAPIEndpoint(apiURL) {
		return "proj_dev", nil
	}
	return "", cliConfigError(
		"project id is required for a non-local Ready CI API; set project in .readyci.yml, READYCI_PROJECT_ID, or --project",
		nil,
	)
}

func localAPIEndpoint(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
