# Plan: Modernize CLI with Charmbracelet

This plan outlines the migration of interactive CLI components from standard library prompts and third-party progress bars to the **Charmbracelet** ecosystem (`huh`, `bubbles`, `lipgloss`).

## Objectives
- Improve User Experience (UX) with styled, accessible interactive components.
- Standardize on a modern TUI framework.
- Maintain compatibility with non-interactive and automated workflows.

## Proposed Changes

### 1. Dependencies
- Add `github.com/charmbracelet/huh` for forms and prompts.
- Add `github.com/charmbracelet/bubbles` for progress bars.
- Add `github.com/charmbracelet/lipgloss` for styling.
- Add `github.com/charmbracelet/bubbletea` as the TUI engine.
- Remove `github.com/schollz/progressbar/v3`.

### 2. Interaction Migration (`cmd/automation.go` & `cmd/pull.go`)
- **Safety Confirmations**: Replace `requireSafetyConfirmation` and `askToContinueOnDownloadError` text prompts with `huh.Confirm`.
- **Conflict Resolution**: Replace the manual choice input in `handlePullConflict` with a `huh.Select` component.
- **Conflict Policies**: Update `resolvePushConflictPolicy` to use `huh.Select`.

### 3. Workspace Initialization (`cmd/init.go`)
- **Guided Setup**: Replace sequential `promptField` calls with a unified `huh.Form`.
- **Security**: Use `huh.Input` with `.EchoMode(huh.EchoModePassword)` for `ATLASSIAN_API_TOKEN`.

### 4. Progress Reporting (`cmd/progress.go`)
- **Component Swap**: Replace `schollz/progressbar` with `bubbles/progress`.
- **UI Logic**: Implement a `bubbletea` model to handle concurrent progress updates and status descriptions.

### 5. Styling and Layout
- Define standard colors and styles using `lipgloss` for consistent warnings, success messages, and headers across the CLI.

## Invariants & Safety
- **Automation**: All `huh` forms and components must respect `flagNonInteractive` and `flagYes`, falling back to default values or failing appropriately without attempting to render a TUI.
- **Output**: Ensure TUI components write to the correct output stream (standard error vs standard out) to avoid polluting piped data.

## Verification
- Run existing E2E tests (`cmd/e2e_test.go`) to ensure sync logic remains intact.
- Manually verify TUI rendering on Windows (PowerShell/CMD).
- Verify that `--non-interactive` still works for CI/CD environments.
