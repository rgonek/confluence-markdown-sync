package converter

import (
	"context"
	"strings"
	"testing"

	adfconv "github.com/rgonek/jira-adf-converter/converter"
)

func TestPlantUMLHandler_ToMarkdown(t *testing.T) {
	ctx := context.Background()
	h := &PlantUMLHandler{}

	puml := "@startuml\n@enduml"
	encoded, _ := h.encodeData(puml)

	// Test case 1: Standard structured parameters
	node1 := adfconv.Node{
		Type: "extension",
		Attrs: map[string]interface{}{
			"extensionKey": "plantumlcloud",
			"parameters": map[string]interface{}{
				"macroParams": map[string]interface{}{
					"filename": map[string]interface{}{"value": "my-diag.puml"},
					"data":     map[string]interface{}{"value": encoded},
				},
			},
		},
	}

	res1, err := h.ToMarkdown(ctx, adfconv.ExtensionRenderInput{Node: node1})
	if err != nil {
		t.Fatalf("ToMarkdown failed: %v", err)
	}
	if !res1.Handled {
		t.Fatal("Expected handled=true")
	}
	if !strings.Contains(res1.Markdown, "@startuml") {
		t.Errorf("Expected markdown to contain @startuml, got %q", res1.Markdown)
	}
	if res1.Metadata["filename"] != "my-diag.puml" {
		t.Errorf("Expected filename my-diag.puml, got %q", res1.Metadata["filename"])
	}

	// Test case 2: Fallback logic for flat parameters
	node2 := adfconv.Node{
		Type: "bodiedExtension",
		Attrs: map[string]interface{}{
			"extensionKey": "plantumlcloud",
			"parameters": map[string]interface{}{
				"filename": "flat.puml",
				"data":     encoded,
			},
		},
	}

	res2, err := h.ToMarkdown(ctx, adfconv.ExtensionRenderInput{Node: node2})
	if err != nil {
		t.Fatalf("ToMarkdown failed: %v", err)
	}
	if !res2.Handled || res2.Metadata["filename"] != "flat.puml" {
		t.Errorf("Failed flat parameter test: handled=%v, filename=%v", res2.Handled, res2.Metadata["filename"])
	}
}

func TestPlantUMLHandler_FromMarkdown(t *testing.T) {
	ctx := context.Background()
	h := &PlantUMLHandler{}

	in := adfconv.ExtensionParseInput{
		ExtensionKey: "plantumlcloud",
		Body:         "```puml\n@startuml\nA -> B\n@enduml\n```",
		Metadata:     map[string]string{"filename": "test.puml"},
	}

	res, err := h.FromMarkdown(ctx, in)
	if err != nil {
		t.Fatalf("FromMarkdown failed: %v", err)
	}
	if !res.Handled {
		t.Fatal("Expected handled=true")
	}

	params := res.Node.Attrs["parameters"].(map[string]interface{})
	macroParams := params["macroParams"].(map[string]interface{})
	if macroParams["filename"].(map[string]interface{})["value"] != "test.puml" {
		t.Errorf("Unexpected filename: %v", macroParams["filename"])
	}

	data := macroParams["data"].(map[string]interface{})["value"].(string)
	if data == "" {
		t.Fatal("Expected non-empty data")
	}

	// Verify we can decode it back
	decoded, err := h.decodeData(data)
	if err != nil {
		t.Fatalf("Failed to decode back: %v", err)
	}
	if !strings.Contains(decoded, "A -> B") {
		t.Errorf("Decoded content mismatch: %q", decoded)
	}
}
