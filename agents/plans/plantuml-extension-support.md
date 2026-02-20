# PlantUML Extension Support with Registry-based Hooks

## Objective
Implement round-trip support for Confluence PlantUML extensions by leveraging the new `pandoc` preset and Extension Hook Registry in `github.com/rgonek/jira-adf-converter`. PlantUML ADF extensions will be transformed into readable Markdown `puml` code blocks (with Pandoc metadata for lossless conversion) during `pull`, and reconstructed into the minimal required ADF extension during `push`.

## Implementation Plan

- [ ] 1. Create a `PlantUMLHandler` that implements the `converter.ExtensionHandler` interface from the converter library (`ToMarkdown` and `FromMarkdown` methods). This handler should encapsulate the logic for Deflate decompression (Base64 -> Raw PUML) and compression (Raw PUML -> Base64).
- [ ] 2. Update `internal/converter/forward.go` to initialize `converter.Config` using the `pandoc` preset and register the `PlantUMLHandler` in the `ExtensionHandlers` map using the extension key `plantumlcloud`. Ensure the handler outputs a `puml` code block utilizing Pandoc attributes (e.g., ` ```puml {filename="..."} `) or Pandoc fenced divs (e.g., `:::{ .adf-extension key="plantumlcloud" }`) to losslessly preserve metadata without relying on HTML comments.
- [ ] 3. Update `internal/converter/reverse.go` to initialize `mdconverter.ReverseConfig` using the `pandoc` preset and register the `PlantUMLHandler` in its `ExtensionHandlers` map. The handler must parse the Pandoc metadata attributes to reconstruct the ADF JSON structure.
- [ ] 4. Update `internal/sync/pull.go` to provide the registered `PlantUMLHandler` to the `ConvertWithContext` call. This ensures that during a space or file pull, any PlantUML macros are accurately converted to Pandoc-flavored source code blocks.
- [ ] 5. Update `internal/sync/push.go` and `internal/sync/validate.go` to provide the registered `PlantUMLHandler` to the reverse `ConvertWithContext` call. This ensures that local edits to PUML blocks are correctly serialized back to Confluence-compatible ADF.
- [ ] 6. Add integration tests in `internal/converter/roundtrip_test.go` or a new `plantuml_test.go` to verify that a PlantUML ADF extension converts to Pandoc Markdown and back to a functionally identical ADF extension (ignoring non-essential IDs).

## Verification Criteria
- `cms pull` transforms `plantumlcloud` extensions into Pandoc-flavored ` ```puml ` blocks capturing the `filename` metadata natively (e.g. via code block attributes or fenced divs), completely avoiding HTML comments.
- `cms push` correctly compresses and encodes the Pandoc ` ```puml ` blocks back into Confluence ADF extensions via the `ExtensionHandler` interface.
- The `filename` attribute is preserved losslessly across a full pull-push cycle.
- Local Markdown previewers ignore Pandoc block attributes but still display the PUML source.
- `cms validate` passes for files containing the new PUML block format.

## Potential Risks and Mitigations
1. **Metadata Corruption**: Users might accidentally delete or edit the Pandoc block attributes. Mitigation: The `PlantUMLHandler` should provide sensible defaults (e.g., a generic filename) if the attributes are missing or malformed.
2. **Library Dependency**: This plan depends on the newly implemented pandoc support and Extension Hooks in `github.com/rgonek/jira-adf-converter`. Mitigation: Ensure the Go module is up-to-date with `main` or tagged before finalizing the integration.

## Alternative Approaches
1. **Fallback JSON extension blocks**: Use the default JSON block behavior from the converter if no handler is registered. Trade-off: Not user-readable or editable like `puml` source blocks.
2. **Post-processing**: Regex replace extensions after the library conversion. Trade-off: Fragile, error-prone, and sidesteps the robust `ExtensionHandler` registry provided by the converter.