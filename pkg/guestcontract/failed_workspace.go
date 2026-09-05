// Package guestcontract contains the small, explicit contract shared with the
// Ready CI guest agent. Keep this package dependency-free: changes to it are a
// rootfs input, while the broader API contract is not.
package guestcontract

const FailedWorkspaceArtifactPath = ".readyci/failed-workspace.tar.gz"

const FailedWorkspaceArtifactRetainedMessage = "readyci: retained failed workspace artifact at " + FailedWorkspaceArtifactPath

const FailedWorkspaceArtifactSkippedMessage = "readyci: skipped failed workspace artifact because it exceeded artifact bundle limit"
