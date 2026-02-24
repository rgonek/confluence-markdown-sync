package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/rgonek/confluence-markdown-sync/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
