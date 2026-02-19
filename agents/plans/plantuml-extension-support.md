# PlantUML Extension Support with Registry-based Hooks

## Objective
Implement round-trip support for Confluence PlantUML extensions by transforming them into readable Markdown `puml` code blocks during `pull` and reconstructing the minimal required ADF extension during `push`.

## Implementation Plan

- [ ] 1. Create a `PlantUMLHandler` that implements the `ExtensionHandler` interface (once defined in the converter library). This handler should encapsulate the logic for Deflate decompression (Base64 -> Raw PUML) and compression (Raw PUML -> Base64).
- [ ] 2. Update `internal/converter/forward.go` to register the `PlantUMLHandler` in the `ForwardConfig` using the extension key `plantumlcloud`. Ensure the handler wraps the output in a `puml` code block preceded by an HTML comment containing the `filename` metadata.
- [ ] 3. Update `internal/converter/reverse.go` to register the `PlantUMLHandler` in the `ReverseConfig`. The handler must be able to parse the HTML metadata comment and the subsequent `puml` code block to reconstruct the minimal ADF JSON structure.
- [ ] 4. Update `internal/sync/pull.go` to provide the registered `PlantUMLHandler` to the `Forward` call. This ensures that during a space or file pull, any PlantUML macros are automatically converted to source code blocks.
- [ ] 5. Update `internal/sync/push.go` and `internal/sync/validate.go` to provide the registered `PlantUMLHandler` to the `Reverse` call. This ensures that local edits to PUML blocks are correctly serialized back to Confluence-compatible ADF.
- [ ] 6. Add integration tests in `internal/converter/roundtrip_test.go` or a new `plantuml_test.go` to verify that a PlantUML ADF extension converts to a Markdown block and back to a functionally identical ADF extension (ignoring non-essential IDs).

## Verification Criteria
- `cms pull` transforms `plantumlcloud` extensions into ` ```puml ` blocks with an accompanying `<!-- cms-extension: ... -->` metadata comment.
- `cms push` correctly compresses and encodes ` ```puml ` blocks back into Confluence ADF extensions.
- The `filename` attribute is preserved across a full pull-push cycle.
- Local Markdown previewers ignore the metadata comment but display the PUML source.
- `cms validate` passes for files containing the new PUML block format.

## Potential Risks and Mitigations
1. **Metadata Corruption**: Users might accidentally delete or edit the HTML comment. Mitigation: The `PlantUMLHandler` should provide sensible defaults (e.g., a generic filename) if the metadata comment is missing or malformed.
2. **Library Dependency**: This plan depends on updates to `github.com/rgonek/jira-adf-converter`. Mitigation: Ensure the converter library is updated and tagged before finalizing the integration in this CLI.

## Alternative Approaches
1. **Hidden PUML attributes**: Use ` ```puml {filename="..."}` instead of HTML comments. Trade-off: Better for some tools, but less portable across all Markdown flavors compared to standard HTML comments.
2. **Post-processing**: Regex replace extensions after the library conversion. Trade-off: Much simpler to implement immediately but fragile and doesn't handle nested structures or multi-extension pages well.
