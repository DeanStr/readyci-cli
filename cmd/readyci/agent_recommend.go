package main

import (
	"github.com/DeanStr/readyci-cli/pkg/config"
)

func recommendAgentCheck(cfg config.ProjectConfig, explicit, goal string) (agentCheckRecommendation, error) {
	return recommendAgentCheckWithIntent(cfg, explicit, goal, agentIntentEditLoop)
}
