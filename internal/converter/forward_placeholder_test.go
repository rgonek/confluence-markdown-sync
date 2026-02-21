package converter

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRemovePlaceholderNodes(t *testing.T) {
	input := []byte(`{
		"type": "doc",
		"version": 1,
		"content": [
			{
				"type": "paragraph",
				"content": [
					{
						"type": "text",
						"text": "Hello "
					},
					{
						"type": "placeholder",
						"attrs": {
							"text": "click here"
						}
					},
					{
						"type": "text",
						"text": "world"
					}
				]
			}
		]
	}`)

	expected := []byte(`{"content":[{"content":[{"text":"Hello ","type":"text"},{"text":"world","type":"text"}],"type":"paragraph"}],"type":"doc","version":1}`)

	out, err := removePlaceholderNodes(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Unmarshal and marshal again to compare without formatting issues
	var outMap map[string]interface{}
	json.Unmarshal(out, &outMap)

	outBytes, _ := json.Marshal(outMap)

	if !bytes.Equal(outBytes, expected) {
		t.Errorf("Expected %s, got %s", expected, outBytes)
	}
}
