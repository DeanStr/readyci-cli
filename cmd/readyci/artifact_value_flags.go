package main

import "strings"

func artifactValueFlag(arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	return name == "--api-url" || name == "--token" || name == "--paths" || name == "--content-sha256"
}

func applyArtifactValueFlag(name, value string, apiURL, token, digest *string, paths *[]string) bool {
	switch name {
	case "--api-url":
		*apiURL = value
	case "--token":
		*token = value
	case "--content-sha256":
		*digest = value
	case "--paths":
		*paths = append(*paths, splitCSV(value)...)
		return true
	}
	return false
}
