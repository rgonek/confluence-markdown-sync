package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rgonek/confluence-markdown-sync/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.ExecuteContext(ctx); err != nil {
		var optionError *cmd.SearchOptionError
		if errors.As(err, &optionError) {
			os.Exit(2) // Cobra already printed the invalid option and its valid values.
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
