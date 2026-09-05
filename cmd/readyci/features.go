package main

import (
	"time"
)

const (
	doctorHealthTimeout     = 5 * time.Second
	doctorFreshWorkerWindow = 15 * time.Minute
)
