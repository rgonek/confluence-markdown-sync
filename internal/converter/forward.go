package converter

import (
	"context"

	adfconv "github.com/rgonek/jira-adf-converter/converter"
)

// ForwardResult holds the result of ADF to Markdown conversion.
type ForwardResult struct {
	Markdown string
	Warnings []adfconv.Warning
}

// ForwardConfig holds configuration for ADF to Markdown conversion.
type ForwardConfig struct {
	LinkHook  adfconv.LinkRenderHook
	MediaHook adfconv.MediaRenderHook
}

// Forward converts ADF JSON to Markdown using best-effort resolution.
// This is used for pull and diff operations where partial success is preferred over failure.
func Forward(ctx context.Context, adfJSON []byte, cfg ForwardConfig, sourcePath string) (ForwardResult, error) {
	// Create converter with best-effort resolution.
	// We want to recover as much content as possible even if some references are broken.
	c, err := adfconv.New(adfconv.Config{
		ResolutionMode: adfconv.ResolutionBestEffort,
		LinkHook:       cfg.LinkHook,
		MediaHook:      cfg.MediaHook,
	})
	if err != nil {
		return ForwardResult{}, err
	}

	// Run conversion with context and source path for relative link resolution.
	res, err := c.ConvertWithContext(ctx, adfJSON, adfconv.ConvertOptions{
		SourcePath: sourcePath,
	})
	if err != nil {
		return ForwardResult{}, err
	}

	return ForwardResult{
		Markdown: res.Markdown,
		Warnings: res.Warnings,
	}, nil
}
