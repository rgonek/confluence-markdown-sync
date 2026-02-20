package converter

import (
	"context"

	adfconv "github.com/rgonek/jira-adf-converter/converter"
	mdconv "github.com/rgonek/jira-adf-converter/mdconverter"
)

// ReverseResult holds the result of Markdown to ADF conversion.
type ReverseResult struct {
	ADF      []byte
	Warnings []adfconv.Warning
}

// ReverseConfig holds configuration for Markdown to ADF conversion.
type ReverseConfig struct {
	LinkHook  mdconv.LinkParseHook
	MediaHook mdconv.MediaParseHook
	Strict    bool
}

// Reverse converts Markdown to ADF JSON.
func Reverse(ctx context.Context, markdown []byte, cfg ReverseConfig, sourcePath string) (ReverseResult, error) {
	mode := mdconv.ResolutionBestEffort
	if cfg.Strict {
		mode = mdconv.ResolutionStrict
	}

	c, err := mdconv.New(mdconv.ReverseConfig{
		ResolutionMode:         mode,
		LinkHook:               cfg.LinkHook,
		MediaHook:              cfg.MediaHook,
		UnderlineDetection:     mdconv.UnderlineDetectPandoc,
		SubSupDetection:        mdconv.SubSupDetectPandoc,
		ColorDetection:         mdconv.ColorDetectPandoc,
		AlignmentDetection:     mdconv.AlignDetectPandoc,
		MentionDetection:       mdconv.MentionDetectPandoc,
		ExpandDetection:        mdconv.ExpandDetectPandoc,
		InlineCardDetection:    mdconv.InlineCardDetectPandoc,
		LayoutSectionDetection: mdconv.LayoutSectionDetectPandoc,
		TableGridDetection:     true,
		ExtensionHandlers: map[string]adfconv.ExtensionHandler{
			"plantumlcloud": &PlantUMLHandler{},
		},
	})
	if err != nil {
		return ReverseResult{}, err
	}

	res, err := c.ConvertWithContext(ctx, string(markdown), mdconv.ConvertOptions{
		SourcePath: sourcePath,
	})
	if err != nil {
		return ReverseResult{}, err
	}

	return ReverseResult{
		ADF:      res.ADF,
		Warnings: res.Warnings,
	}, nil
}
