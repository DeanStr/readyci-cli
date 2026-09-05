package main

import "fmt"

func agentCommand(args []string) error {
	if len(args) == 0 {
		return cliConfigError("usage: readyci agent <plan|explain|artifacts|setup|instructions|token>", nil)
	}
	switch args[0] {
	case "plan":
		return agentPlanCommand(args[1:])
	case "explain":
		return agentExplainCommand(args[1:])
	case "artifacts":
		return agentArtifactsCommand(args[1:])
	case "setup":
		return agentSetupCommand(args[1:])
	case "instructions":
		return agentInstructionsCommand(args[1:])
	case "token":
		return agentTokenCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown agent command %q", args[0]), nil)
	}
}
