package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/DeanStr/readyci-cli/internal/api"
	"github.com/DeanStr/readyci-cli/pkg/guestcontract"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

// The session owns scheduling and outcomes. This observer only reads durable
// output, retaining a separate cursor for every matrix child and retry attempt.
type executionLogObserver struct {
	stdout, stderr io.Writer
	cursors        map[string]int64
	finished       map[string]bool
	warned         map[string]bool
	lastRun        string
	redactor       cliSecretRedactor
	streams        map[string]map[string]*executionLogRedactor
	workspaces     map[string]*executionWorkspaceOutput
}

func executionLogsForOutput(jsonOutput bool, token string) *executionLogObserver {
	if jsonOutput {
		return nil
	}
	return &executionLogObserver{stdout: os.Stdout, stderr: os.Stderr, redactor: newCLISecretRedactor([]string{"--token", token})}
}

func (o *executionLogObserver) observe(ctx context.Context, client *api.Client, session preflightapi.VerificationSession) error {
	if o == nil || session.Recipe.Purpose != preflightapi.ExecutionPurpose {
		return nil
	}
	if o.cursors == nil {
		o.cursors = make(map[string]int64)
		o.finished = make(map[string]bool)
		o.warned = make(map[string]bool)
		o.streams = make(map[string]map[string]*executionLogRedactor)
		o.workspaces = make(map[string]*executionWorkspaceOutput)
	}
	for _, node := range session.Nodes {
		for _, attempt := range node.Attempts {
			if attempt.RunID == "" {
				return fmt.Errorf("execution attempt has no run ID")
			}
			if o.finished[attempt.RunID] {
				continue
			}
			terminal := preflightapi.IsTerminalRunStatus(attempt.Status)
			for {
				cursor := o.cursors[attempt.RunID]
				var page preflightapi.RunEventsResponse
				err := retryObservationRead(ctx, func() error {
					var err error
					page, err = client.ListRunEventsPage(ctx, attempt.RunID, cursor, 128)
					return err
				})
				if err != nil {
					return fmt.Errorf("read output for run %s: %w", attempt.RunID, err)
				}
				if page.RunID != attempt.RunID || page.NextCursor < cursor || (page.HasMore && page.NextCursor <= cursor) {
					return fmt.Errorf("invalid output page for run %s at cursor %d", attempt.RunID, cursor)
				}
				for _, event := range page.Events {
					if event.ID <= cursor || event.ID > page.NextCursor {
						return fmt.Errorf("invalid output event cursor for run %s", attempt.RunID)
					}
					cursor = event.ID
					switch event.Type {
					case preflightapi.EventStdout, preflightapi.EventStderr:
						data, err := event.DataBytes()
						if err != nil {
							return fmt.Errorf("decode output for run %s: %w", attempt.RunID, err)
						}
						if len(data) == 0 {
							continue
						}
						if event.Type == preflightapi.EventStderr {
							o.observeWorkspaceOutput(attempt.RunID, string(data))
						}
						stream := o.logStream(attempt.RunID, event.Type)
						if err := o.writeOutput(node, attempt, event.Type, stream.redact(string(data), false)); err != nil {
							return err
						}

					}
				}
				if page.Truncated && !o.warned[attempt.RunID] {
					if _, err := fmt.Fprintf(o.stderr, "readyci: output for run %s was truncated by the server\n", attempt.RunID); err != nil {
						return err
					}
					o.warned[attempt.RunID] = true
				}
				o.cursors[attempt.RunID] = page.NextCursor
				if !page.HasMore {
					if terminal {
						for _, eventType := range []string{preflightapi.EventStdout, preflightapi.EventStderr} {
							if stream := o.streams[attempt.RunID][eventType]; stream != nil {
								if err := o.writeOutput(node, attempt, eventType, stream.redact("", true)); err != nil {
									return err
								}
							}
						}
						delete(o.streams, attempt.RunID)
					}
					o.finished[attempt.RunID] = terminal
					break
				}
				// Bound each live poll; drain terminal attempts before returning results.
				if !terminal {
					break
				}
			}
		}
	}
	return nil
}

func (o *executionLogObserver) logStream(runID, eventType string) *executionLogRedactor {
	if o.streams[runID] == nil {
		o.streams[runID] = make(map[string]*executionLogRedactor)
	}
	if o.streams[runID][eventType] == nil {
		o.streams[runID][eventType] = &executionLogRedactor{redactor: o.redactor}
	}
	return o.streams[runID][eventType]
}

func (o *executionLogObserver) writeOutput(node preflightapi.VerificationNode, attempt preflightapi.VerificationAttempt, eventType, data string) error {
	if data == "" {
		return nil
	}
	if o.lastRun != attempt.RunID {
		if _, err := fmt.Fprintf(o.stderr, "\nreadyci: %s (attempt %d, %s)\n", sanitizeAgentProgressField(node.Name), attempt.Attempt, attempt.RunID); err != nil {
			return err
		}
		o.lastRun = attempt.RunID
	}
	writer := o.stdout
	if eventType == preflightapi.EventStderr {
		writer = o.stderr
	}
	if _, err := io.WriteString(writer, data); err != nil {
		return fmt.Errorf("write output for run %s: %w", attempt.RunID, err)
	}
	return nil
}

// Retention messages can span event pages. A download hint also requires an
// available uploaded artifact; requesting --keep-failed alone is insufficient.
type executionWorkspaceOutput struct {
	buffer            string
	retained, skipped bool
}

func (o *executionLogObserver) observeWorkspaceOutput(runID, data string) {
	if o.workspaces[runID] == nil {
		o.workspaces[runID] = &executionWorkspaceOutput{}
	}
	state := o.workspaces[runID]
	state.buffer += data
	state.retained = state.retained || strings.Contains(state.buffer, guestcontract.FailedWorkspaceArtifactRetainedMessage)
	state.skipped = state.skipped || strings.Contains(state.buffer, guestcontract.FailedWorkspaceArtifactSkippedMessage)
	state.buffer = trimStreamMarkerBuffer(state.buffer)
}

func (o *executionLogObserver) retainedWorkspace(runID string) bool {
	if o == nil {
		return false
	}
	state := o.workspaces[runID]
	return state != nil && state.retained && !state.skipped
}
