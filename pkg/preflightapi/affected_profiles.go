package preflightapi

// Affected profile names are stable API/config identifiers. The CLI keeps
// docs, Go, Ready CI config, and shell rules as an offline compatibility
// baseline; the richer profile names remain public only so existing payloads
// and hosted recommendations can refer to them without exposing hosted packs.
const (
	AffectedProfileDocs          = "docs"
	AffectedProfileGo            = "go"
	AffectedProfileGoPostgres    = "go-postgres"
	AffectedProfileMigrations    = "migrations"
	AffectedProfileOpenAPI       = "openapi"
	AffectedProfileOps           = "ops"
	AffectedProfileReadyCI       = "readyci"
	AffectedProfileRepoConfig    = "repo-config"
	AffectedProfileRustWorkspace = "rust-workspace"
	AffectedProfileShell         = "shell"
	AffectedProfileViteReact     = "vite-react"
)
