// Package blevestore implements the search.Store interface backed by Bleve.
package blevestore

import (
	"github.com/blevesearch/bleve/v2/mapping"
)

// NewMapping returns the Bleve IndexMapping for the confluence search index.
//
// Field categories:
//   - keyword  (exact match, keyword analyzer): type, path, page_id, space_key, labels, language
//   - text     (standard english analyzer):     title, content, heading_text, heading_path_text
//   - numeric:  heading_level, line
//   - datetime: mod_time
func NewMapping() mapping.IndexMapping {
	im := mapping.NewIndexMapping()
	im.DefaultAnalyzer = "en"
	// Disable dynamic indexing so only explicitly mapped fields are indexed.
	im.DefaultMapping.Dynamic = false

	// --- keyword fields ---
	kw := mapping.NewKeywordFieldMapping()
	kw.Store = true

	// --- text fields ---
	textField := func() *mapping.FieldMapping {
		fm := mapping.NewTextFieldMapping()
		fm.Analyzer = "en"
		fm.Store = true
		fm.IncludeTermVectors = true
		return fm
	}

	// --- numeric field ---
	num := mapping.NewNumericFieldMapping()
	num.Store = true

	// --- datetime field ---
	dt := mapping.NewDateTimeFieldMapping()
	dt.Store = true

	dm := mapping.NewDocumentMapping()
	dm.Dynamic = false

	// keyword fields
	dm.AddFieldMappingsAt("type", kw)
	dm.AddFieldMappingsAt("path", kw)
	dm.AddFieldMappingsAt("page_id", kw)
	dm.AddFieldMappingsAt("space_key", kw)
	dm.AddFieldMappingsAt("labels", kw)
	dm.AddFieldMappingsAt("language", kw)

	// text fields
	dm.AddFieldMappingsAt("title", textField())
	dm.AddFieldMappingsAt("content", textField())
	dm.AddFieldMappingsAt("heading_text", textField())
	dm.AddFieldMappingsAt("heading_path_text", textField())

	// numeric fields
	dm.AddFieldMappingsAt("heading_level", num)
	dm.AddFieldMappingsAt("line", num)

	// datetime field
	dm.AddFieldMappingsAt("mod_time", dt)

	// keyword fields — author names for exact-match filtering
	dm.AddFieldMappingsAt("created_by", kw)
	dm.AddFieldMappingsAt("updated_by", kw)

	// datetime fields — creation/update timestamps for date-range filtering
	dm.AddFieldMappingsAt("created_at", dt)
	dm.AddFieldMappingsAt("updated_at", dt)

	im.DefaultMapping = dm

	return im
}

// allDocFields is the list of stored fields to retrieve on a Search hit.
var allDocFields = []string{
	"type", "path", "page_id", "space_key", "labels",
	"language", "title", "content", "heading_text", "heading_path_text",
	"heading_level", "line", "mod_time",
	"created_by", "created_at", "updated_by", "updated_at",
}
