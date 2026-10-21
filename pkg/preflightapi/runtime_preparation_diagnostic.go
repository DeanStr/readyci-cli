package preflightapi

// RuntimePreparationFailureMarker is emitted on stderr in the prepare phase
// when the worker's runtime bootstrap exits 78. It is diagnostic evidence, never
// authority to change a terminal result or establish verification coverage.
const RuntimePreparationFailureMarker = `{"schema_version":"readyci.runtime_preparation.v1","status":"failed"}`
