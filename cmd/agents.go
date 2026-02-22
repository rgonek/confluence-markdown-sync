package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/spf13/cobra"
)

func newAgentsCmd() *cobra.Command {
	var templateType string

	cmd := &cobra.Command{
		Use:   "agents [TARGET]",
		Short: "Initialize an AGENTS.md file for a space",
		Long: `agents creates an AGENTS.md file in the target space directory with instructions for AI agents.
		
You can choose from several templates using the --type flag:
- technical-documentation (default): Optimized for engineering docs, diagrams, and API references.
- hr-info: Optimized for internal policies, HR info, and general documentation.
- project-management: Optimized for roadmaps, meeting notes, and status reports.
- product-requirements: Optimized for PRDs, user stories, and acceptance criteria.
- customer-support: Optimized for knowledge base articles, FAQs, and troubleshooting guides.
- general: A minimal template with core sync rules.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			return runAgentsInit(cmd, config.ParseTarget(raw), templateType)
		},
	}

	cmd.Flags().StringVarP(&templateType, "type", "t", "technical-documentation", "Template type: technical-documentation, hr-info, project-management, product-requirements, customer-support, general")
	return cmd
}

func runAgentsInit(cmd *cobra.Command, target config.Target, templateType string) error {
	out := cmd.OutOrStdout()

	// Resolve the space directory
	initialCtx, err := resolveInitialPullContext(target)
	if err != nil {
		return err
	}
	spaceDir := initialCtx.spaceDir
	spaceKey := initialCtx.spaceKey

	if _, err := os.Stat(spaceDir); os.IsNotExist(err) {
		return fmt.Errorf("space directory %s does not exist; run 'cms pull %s' first", spaceDir, spaceKey)
	}

	path := filepath.Join(spaceDir, "AGENTS.md")
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(out, "AGENTS.md already exists in %s. Skipping.\n", spaceDir)
		return nil
	}

	var content string
	switch strings.ToLower(templateType) {
	case "technical-documentation", "tech":
		content = getTechAgentsTemplate(spaceKey)
	case "hr-info", "hr":
		content = getHRAgentsTemplate(spaceKey)
	case "project-management", "pm", "projects":
		content = getPMAgentsTemplate(spaceKey)
	case "product-requirements", "prd", "product":
		content = getPRDAgentsTemplate(spaceKey)
	case "customer-support", "support", "kb":
		content = getSupportAgentsTemplate(spaceKey)
	case "general":
		content = getGeneralAgentsTemplate(spaceKey)
	default:
		return fmt.Errorf("invalid template type %q; supported: technical-documentation, hr-info, project-management, product-requirements, customer-support, general", templateType)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write AGENTS.md: %w", err)
	}

	fmt.Fprintf(out, "✓ AGENTS.md (%s) created in %s\n", templateType, spaceDir)
	return nil
}

func getTechAgentsTemplate(spaceKey string) string {
	return fmt.Sprintf(`# AGENTS (Technical Documentation: %s)

This space directory contains technical documentation for [%s].

## Agent Role
You are a technical writer and software engineer. Your goal is to maintain high-quality, accurate, and developer-friendly documentation.

## Space-Specific Rules
- **Diagrams**: Use Mermaid or PlantUML for all architecture diagrams.
- **Code Snippets**: Always specify the language for syntax highlighting.
- **API Docs**: Ensure all endpoints include request/response examples.
- **Links**: Use relative Markdown links for cross-references between pages.
- **Assets**: Store all images in the `+"`assets/`"+` directory.

## Sync Workflow
1. `+"`cms pull`"+` to get the latest state.
2. Edit Markdown files.
3. `+"`cms validate`"+` to check links and ADF compatibility.
4. `+"`cms push`"+` to publish.
`, spaceKey, spaceKey)
}

func getHRAgentsTemplate(spaceKey string) string {
	return fmt.Sprintf(`# AGENTS (HR & Internal Info: %s)

This space directory contains HR policies and internal company information for [%s].

## Agent Role
You are an internal communications specialist. Your goal is to ensure documentation is clear, inclusive, and follows company tone-of-voice guidelines.

## Space-Specific Rules
- **Privacy**: NEVER include PII (Personally Identifiable Information) such as private phone numbers or home addresses.
- **Formatting**: Use bold text for key terms and bullet points for readability.
- **Tone**: Maintain a professional yet welcoming tone.
- **Links**: Ensure all links to external portals (Workday, etc.) are up to date.

## Sync Workflow
1. `+"`cms pull`"+`
2. Update policy Markdown.
3. `+"`cms validate`"+`
4. `+"`cms push`"+`
`, spaceKey, spaceKey)
}

func getPMAgentsTemplate(spaceKey string) string {
	return fmt.Sprintf(`# AGENTS (Project Management: %s)

This space directory contains project roadmaps, meeting notes, and status reports for [%s].

## Agent Role
You are a project manager. Your goal is to keep stakeholders informed and ensure project artifacts are structured for easy scanning and tracking.

## Space-Specific Rules
- **Meeting Notes**: Use a consistent header format: Date, Attendees, Agenda, Action Items.
- **Action Items**: Use checklist format `+"`- [ ]`"+` for tasks.
- **Roadmaps**: Use tables for high-level project timelines and milestones.
- **Status Updates**: Use traffic light emojis (🟢, 🟡, 🔴) to indicate project health.

## Sync Workflow
1. `+"`cms pull`"+`
2. Update status/notes.
3. `+"`cms validate`"+`
4. `+"`cms push`"+`
`, spaceKey, spaceKey)
}

func getPRDAgentsTemplate(spaceKey string) string {
	return fmt.Sprintf(`# AGENTS (Product Requirements: %s)

This space directory contains PRDs, User Stories, and Design Specs for [%s].

## Agent Role
You are a product manager. Your goal is to define clear, actionable requirements that bridge the gap between business needs and technical implementation.

## Space-Specific Rules
- **User Stories**: Follow the format: "As a [user], I want to [action], so that [value]".
- **Acceptance Criteria**: Use numbered lists for explicit testable conditions.
- **Design Links**: Always include links to Figma/Sketch prototypes where applicable.
- **Prioritization**: Clearly mark "Must Have", "Should Have", and "Could Have" features.

## Sync Workflow
1. `+"`cms pull`"+`
2. Refine requirements.
3. `+"`cms validate`"+`
4. `+"`cms push`"+`
`, spaceKey, spaceKey)
}

func getSupportAgentsTemplate(spaceKey string) string {
	return fmt.Sprintf(`# AGENTS (Customer Support & KB: %s)

This space directory contains knowledge base articles and FAQs for [%s].

## Agent Role
You are a support specialist and technical communicator. Your goal is to solve user problems quickly with clear, step-by-step instructions.

## Space-Specific Rules
- **Clarity**: Use simple, non-jargon language. Define technical terms if they must be used.
- **Step-by-Step**: Use numbered lists for procedures.
- **Troubleshooting**: Always include a "Symptoms" and "Resolution" section.
- **Callouts**: Use bolding or blockquotes for critical warnings or tips.

## Sync Workflow
1. `+"`cms pull`"+`
2. Update help articles.
3. `+"`cms validate`"+`
4. `+"`cms push`"+`
`, spaceKey, spaceKey)
}

func getGeneralAgentsTemplate(spaceKey string) string {
	return fmt.Sprintf(`# AGENTS (%s)

This space directory is managed by `+"`cms`"+`.

## Rules
- Do not edit `+"`id`"+` or `+"`space`"+` in frontmatter.
- Always `+"`pull`"+` before `+"`push`"+`.
- Run `+"`validate`"+` before publishing.

## Commands
- `+"`cms pull`"+`
- `+"`cms push`"+`
- `+"`cms validate`"+`
`, spaceKey)
}
