package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/internal/artifactapply"
	"github.com/DeanStr/readyci-cli/pkg/artifactbundle"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

const maxAgentEvidencePacks = 20

func evidencePacksFromArtifactFiles(apiURL, runID string, files []artifactbundle.File) []agentEvidencePack {
	byKindAndPath := make(map[string]agentEvidencePack)
	var keys []string
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		kind, description, ok := evidencePackKindForPath(path)
		if !ok {
			continue
		}
		groupPath := evidencePackGroupPath(path, kind)
		key := kind + "\x00" + groupPath
		pack, exists := byKindAndPath[key]
		if !exists {
			keys = append(keys, key)
			pack = agentEvidencePack{
				Name:           evidencePackName(kind, groupPath),
				Kind:           kind,
				Path:           groupPath,
				Source:         "artifact_file",
				PreviewCommand: agentArtifactPlanShellCommand(apiURL, runID, []string{groupPath}),
				Description:    description,
			}
		}
		pack.SizeBytes += file.SizeBytes
		if len(pack.Files) < 10 {
			pack.Files = append(pack.Files, path)
		}
		byKindAndPath[key] = pack
	}
	sort.Strings(keys)
	packs := make([]agentEvidencePack, 0, len(keys))
	for _, key := range keys {
		packs = append(packs, byKindAndPath[key])
	}
	return trimEvidencePacks(packs)
}

func evidencePacksFromRunArtifactFiles(ctx context.Context, client *api.Client, apiURL, runID string, artifactPaths []string, artifacts []preflightapi.ArtifactSummary) ([]agentEvidencePack, error) {
	paths := uniqueNonEmptyStrings(artifactPaths)
	if len(artifacts) == 0 || len(paths) == 0 {
		return nil, nil
	}
	tmp, err := os.CreateTemp("", ".readyci-evidence-*.tar.gz")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpPath)
	}()
	if err := client.DownloadArtifactArchive(ctx, runID, tmp); err != nil {
		return nil, err
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		return nil, err
	}
	manifest, err := artifactapply.Apply(ctx, os.TempDir(), paths, tmp, artifactapply.ApplyOptions{DryRun: true})
	if err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	closed = true
	packs := evidencePacksFromArtifactFiles(apiURL, runID, manifest.Files)
	artifact := firstArtifactSummary(artifacts)
	for i := range packs {
		packs[i].ArtifactID = artifact.ID
		packs[i].DownloadURL = artifact.DownloadURL
	}
	return packs, nil
}

func evidencePackKindForPath(path string) (string, string, bool) {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	lower = strings.TrimPrefix(lower, "./")
	switch {
	case lower == "", lower == "artifacts.tar.gz", lower == "empty-artifacts.tar.gz":
		return "", "", false
	case strings.Contains(lower, "readyci.evidence.json") || strings.Contains(lower, "evidence-pack"):
		return "readyci_evidence", "Structured Ready CI evidence pack for agent consumption.", true
	case strings.Contains(lower, "playwright-report"):
		return "playwright_report", "Playwright HTML report with browser journey diagnostics.", true
	case strings.Contains(lower, "trace") && (strings.Contains(lower, "playwright") || strings.HasSuffix(lower, ".zip")):
		return "playwright_trace", "Browser trace suitable for replaying the failing UI journey.", true
	case strings.Contains(lower, "screenshot") || hasAnySuffix(lower, ".png", ".jpg", ".jpeg"):
		return "screenshot", "Screenshot evidence captured during verification.", true
	case strings.Contains(lower, "video") || hasAnySuffix(lower, ".webm", ".mp4", ".mov"):
		return "video", "Video evidence captured during verification.", true
	case strings.Contains(lower, "junit") || hasAnySuffix(lower, ".junit.xml", ".xml.gz") || strings.HasSuffix(lower, "test-results.xml"):
		return "junit", "JUnit-compatible test report.", true
	case strings.Contains(lower, "coverage") || strings.Contains(lower, "lcov"):
		return "coverage", "Coverage output retained for inspection.", true
	case strings.Contains(lower, "test-results") || strings.Contains(lower, "test-results/"):
		return "test_results", "Test-result bundle with runner-specific diagnostics.", true
	case pathHasSegment(lower, "logs") || hasAnySuffix(lower, ".log", ".log.json", ".jsonl"):
		return "logs", "Log evidence retained as an artifact.", true
	default:
		return "", "", false
	}
}

func evidencePackGroupPath(path, kind string) string {
	normalized := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(path)), "./")
	if normalized == "" {
		return path
	}
	switch kind {
	case "playwright_report":
		if idx := strings.Index(strings.ToLower(normalized), "playwright-report/"); idx >= 0 {
			return normalized[:idx] + "playwright-report/**"
		}
	case "test_results":
		if idx := strings.Index(strings.ToLower(normalized), "test-results/"); idx >= 0 {
			return normalized[:idx] + "test-results/**"
		}
	case "coverage":
		if idx := strings.Index(strings.ToLower(normalized), "coverage/"); idx >= 0 {
			return normalized[:idx] + "coverage/**"
		}
	case "logs":
		return logEvidencePackGroupPath(normalized)
	}
	return normalized
}

func logEvidencePackGroupPath(normalized string) string {
	parts := strings.Split(normalized, "/")
	for i, part := range parts {
		if strings.EqualFold(part, "logs") && i < len(parts)-1 {
			return strings.Join(parts[:i+1], "/") + "/**"
		}
	}
	return normalized
}

func pathHasSegment(path, segment string) bool {
	segment = strings.ToLower(strings.TrimSpace(segment))
	if segment == "" {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if strings.ToLower(strings.TrimSpace(part)) == segment {
			return true
		}
	}
	return false
}

func evidencePackName(kind, path string) string {
	label := strings.ReplaceAll(kind, "_", " ")
	if label == "" {
		label = "artifact"
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return label
	}
	return fmt.Sprintf("%s: %s", label, path)
}

func firstArtifactSummary(artifacts []preflightapi.ArtifactSummary) preflightapi.ArtifactSummary {
	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.ID) != "" || strings.TrimSpace(artifact.DownloadURL) != "" {
			return artifact
		}
	}
	return preflightapi.ArtifactSummary{}
}

func uniqueEvidencePacks(packs []agentEvidencePack) []agentEvidencePack {
	seen := make(map[string]bool, len(packs))
	out := make([]agentEvidencePack, 0, len(packs))
	for _, pack := range packs {
		key := pack.Kind + "\x00" + pack.Path + "\x00" + pack.ArtifactID
		if strings.TrimSpace(pack.Kind) == "" || strings.TrimSpace(pack.Path) == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, pack)
	}
	return out
}

func trimEvidencePacks(packs []agentEvidencePack) []agentEvidencePack {
	if len(packs) <= maxAgentEvidencePacks {
		return packs
	}
	return append([]agentEvidencePack(nil), packs[:maxAgentEvidencePacks]...)
}

func agentArtifactPlanShellCommand(apiURL, runID string, paths []string) string {
	command := "readyci agent artifacts plan"
	command += apiURLFlag(apiURL)
	command += " --json " + shellQuoteArg(strings.TrimSpace(runID))
	for _, path := range uniqueNonEmptyStrings(paths) {
		command += " --paths " + shellQuoteArg(path)
	}
	return command
}

func hasAnySuffix(value string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}
