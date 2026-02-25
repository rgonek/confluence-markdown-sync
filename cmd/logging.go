package cmd

import (
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"
)

var runIDNowUTC = func() time.Time {
	return time.Now().UTC()
}

var runIDSequence uint64

func beginCommandRun(commandName string) (string, func()) {
	cleanCommand := strings.TrimSpace(strings.ToLower(commandName))
	if cleanCommand == "" {
		cleanCommand = "unknown"
	}

	runID := nextRunID(cleanCommand)
	previous := slog.Default()
	slog.SetDefault(previous.With("run_id", runID, "command", cleanCommand))

	return runID, func() {
		slog.SetDefault(previous)
	}
}

func nextRunID(commandName string) string {
	sequence := atomic.AddUint64(&runIDSequence, 1)
	timestamp := runIDNowUTC().Format("20060102T150405.000000000Z")
	return fmt.Sprintf("%s-%s-%06d", commandName, timestamp, sequence)
}
