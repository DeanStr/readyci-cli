package main

import (
	"fmt"
	"github.com/DeanStr/readyci-cli/pkg/preflightapi"
)

func printRunTimings(timings preflightapi.RunTimings) {
	for _, phase := range []struct {
		name  string
		value *int64
	}{{"queue", timings.QueueMS}, {"worker preparation", timings.WorkerPreparationMS}, {"command (including preparation)", timings.CommandMS}, {"finalization", timings.FinalizationMS}} {
		if phase.value == nil {
			fmt.Printf("timing[%s]: unavailable\n", phase.name)
		} else {
			fmt.Printf("timing[%s]: %dms\n", phase.name, *phase.value)
		}
	}
}
