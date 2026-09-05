package main

import (
	"fmt"
)

func adminCommand(args []string) error {
	if len(args) == 0 {
		printUsage()
		return cliConfigError("usage: readyci admin <runs|inspect|events|logs|cancel|tokens|teams|workers|queue|storage|privacy>", nil)
	}
	switch args[0] {
	case "runs":
		return adminRunsCommand(args[1:])
	case "inspect":
		return adminInspectCommand(args[1:])
	case "events":
		return adminEventsCommand(args[1:])
	case "logs":
		return adminLogsCommand(args[1:])
	case "cancel":
		return adminCancelCommand(args[1:])
	case "tokens":
		return adminTokensCommand(args[1:])
	case "teams":
		return adminTeamsCommand(args[1:])
	case "workers":
		return adminWorkersCommand(args[1:])
	case "queue":
		return adminQueueCommand(args[1:])
	case "storage":
		return adminStorageCommand(args[1:])
	case "privacy":
		return adminPrivacyCommand(args[1:])
	default:
		printUsage()
		return cliConfigError(fmt.Sprintf("unknown admin command %q", args[0]), nil)
	}
}
