package preflightapi

import "github.com/DeanStr/readyci-cli/pkg/guestcontract"

// RuntimePreparationFailureMarker is emitted on stderr in the prepare phase
// when the worker's runtime bootstrap exits 78. It is diagnostic evidence, never
// authority to change a terminal result or establish verification coverage.
const RuntimePreparationFailureMarker = guestcontract.RuntimePreparationFailureMarker
