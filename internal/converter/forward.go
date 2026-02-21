package converter

import (
	"context"
	"encoding/json"

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
		ResolutionMode:       adfconv.ResolutionBestEffort,
		LinkHook:             cfg.LinkHook,
		MediaHook:            cfg.MediaHook,
		UnderlineStyle:       adfconv.UnderlinePandoc,
		SubSupStyle:          adfconv.SubSupPandoc,
		TextColorStyle:       adfconv.ColorPandoc,
		BackgroundColorStyle: adfconv.ColorPandoc,
		MentionStyle:         adfconv.MentionPandoc,
		AlignmentStyle:       adfconv.AlignPandoc,
		ExpandStyle:          adfconv.ExpandPandoc,
		InlineCardStyle:      adfconv.InlineCardPandoc,
		LayoutSectionStyle:   adfconv.LayoutSectionPandoc,
		TableMode:            adfconv.TableAutoPandoc,
		ExtensionHandlers: map[string]adfconv.ExtensionHandler{
			"plantumlcloud": &PlantUMLHandler{},
		},
	})
	if err != nil {
		return ForwardResult{}, err
	}

	cleanedJSON, err := removePlaceholderNodes(adfJSON)
	if err != nil {
		// Fallback to original JSON if preprocessing fails
		cleanedJSON = adfJSON
	}

	// Run conversion with context and source path for relative link resolution.
	res, err := c.ConvertWithContext(ctx, cleanedJSON, adfconv.ConvertOptions{
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

// removePlaceholderNodes unmarshals the ADF JSON, walks the tree to remove any node
// with "type": "placeholder", and then marshals it back to JSON.
func removePlaceholderNodes(adfJSON []byte) ([]byte, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(adfJSON, &doc); err != nil {
		return nil, err
	}

	doc = walkAndRemovePlaceholders(doc).(map[string]interface{})

	return json.Marshal(doc)
}

func walkAndRemovePlaceholders(node interface{}) interface{} {
	switch v := node.(type) {
	case map[string]interface{}:
		// Check if it's a node with content
		if content, ok := v["content"].([]interface{}); ok {
			var newContent []interface{}
			for _, child := range content {
				if childMap, ok := child.(map[string]interface{}); ok {
					if nodeType, ok := childMap["type"].(string); ok && nodeType == "placeholder" {
						continue // Skip this node entirely
					}
				}
				// Recursively process the child and keep it
				newContent = append(newContent, walkAndRemovePlaceholders(child))
			}
			v["content"] = newContent
		}

		// Process marks
		if marks, ok := v["marks"].([]interface{}); ok {
			var newMarks []interface{}
			for _, mark := range marks {
				newMarks = append(newMarks, walkAndRemovePlaceholders(mark))
			}
			v["marks"] = newMarks
		}

		// Process other nested objects
		for key, val := range v {
			if key != "content" && key != "marks" {
				v[key] = walkAndRemovePlaceholders(val)
			}
		}

		return v
	case []interface{}:
		var newArr []interface{}
		for _, item := range v {
			newArr = append(newArr, walkAndRemovePlaceholders(item))
		}
		return newArr
	default:
		return v
	}
}
