package main

import (
	"errors"
	"os"
	"strings"
)

func upsertMarkedBlock(path, block string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content := string(data)
	block = strings.TrimRight(block, "\n") + "\n"
	start := strings.Index(content, agentInstructionsStart)
	end := strings.Index(content, agentInstructionsEnd)
	if start >= 0 && end >= start {
		end += len(agentInstructionsEnd)
		next := content[:start] + strings.TrimRight(block, "\n") + content[end:]
		if !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		return os.WriteFile(path, []byte(next), 0o644)
	}
	if strings.TrimSpace(content) != "" && !strings.HasSuffix(content, "\n\n") {
		if strings.HasSuffix(content, "\n") {
			content += "\n"
		} else {
			content += "\n\n"
		}
	}
	return os.WriteFile(path, []byte(content+block), 0o644)
}
