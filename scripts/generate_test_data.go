package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	tdSpace = "TD"
	sdSpace = "SD"
)

func main() {
	basePath := "D:/confluence-test-final"
	rand.Seed(time.Now().UnixNano())

	// 1. Wipe local directories to ensure no old files remain
	os.RemoveAll(filepath.Join(basePath, "Technical documentation (TD)"))
	os.RemoveAll(filepath.Join(basePath, "Software development (SD)"))
	os.MkdirAll(filepath.Join(basePath, "Technical documentation (TD)"), 0755)
	os.MkdirAll(filepath.Join(basePath, "Software development (SD)"), 0755)

	// 2. Generate TD Space
	generateSpace(filepath.Join(basePath, "Technical documentation (TD)"), tdSpace, "Technical Documentation", getTDHighQualityTopics())

	// 3. Generate SD Space
	generateSpace(filepath.Join(basePath, "Software development (SD)"), sdSpace, "Software Development", getSDHighQualityTopics())
}

func getTDHighQualityTopics() map[string][]string {
	return map[string][]string{
		"Core Systems": {
			"Authentication Service Deep Dive",
			"Global Load Balancing Strategy",
			"Data Lake Architecture and ETL Pipeline",
			"Distributed Caching with Redis Cluster",
			"Message Queue Reliability Patterns",
			"Legacy System Integration Layer",
			"Search Indexing Engine Internals",
			"Notification Dispatcher Service",
			"User Profile Management Service",
			"Metadata Governance Framework",
		},
		"Infrastructure": {
			"Kubernetes Networking with Cilium",
			"Terraform State Management Best Practices",
			"AWS Multi-Account Strategy",
			"Prometheus Monitoring and Alerting Rules",
			"Database Sharding Implementation",
			"Auto-scaling Policies for Web Tier",
			"Identity and Access Management Guide",
			"Network Partition Recovery Procedures",
			"CI-CD Pipeline Security Hardening",
			"Infrastructure as Code Testing Strategy",
		},
		"Security": {
			"Zero Trust Network Implementation",
			"Secret Rotation Automation",
			"Vulnerability Scanning Lifecycle",
			"Compliance Framework for Fintech",
			"Incident Response Plan 2026",
			"Encryption at Rest Implementation",
			"OIDC Provider Configuration",
			"Security Auditing and Logging",
			"Threat Modeling for Microservices",
			"WAF Rule Management and Tuning",
		},
		"Development": {
			"Standard Library for Go Services",
			"Frontend Design System Guide",
			"Mobile SDK Integration Patterns",
			"Unit Testing and Mocking Standards",
			"GraphQL API Schema Design",
			"Event-Sourcing in Microservices",
			"Documentation as Code Principles",
			"Code Review Guidelines",
			"Dependency Management Policy",
			"Performance Profiling and Tuning",
		},
		"Reliability": {
			"Chaos Engineering Experiments",
			"Service Level Objectives (SLO) Definitions",
			"On-call Handover and Escalation",
			"Post-Mortem Analysis Process",
			"Capacity Planning for Q3",
			"Traffic Mirroring and Shadow Testing",
			"Circuit Breaker Implementation",
			"Rate Limiting Strategy for Public APIs",
			"Database Backup Verification",
			"Backup and Recovery Drills",
		},
	}
}

func getSDHighQualityTopics() map[string][]string {
	return map[string][]string{
		"Product Requirements": {
			"PRD: Multi-Factor Authentication Redesign",
			"PRD: Real-time Collaboration Dashboard",
			"PRD: Automated Billing for Tiered Subscriptions",
			"PRD: Advanced Search Filters and Facets",
			"PRD: Mobile Offline Mode Support",
			"PRD: External Partner API Portal",
			"PRD: Enterprise Single Sign-On (SSO)",
			"PRD: User Onboarding Flow Optimization",
			"PRD: Data Export and Portability Tools",
			"PRD: Dark Mode and Accessibility Audit",
		},
		"Meeting Notes": {
			"2026-02-20 Weekly Engineering Sync",
			"2026-02-18 Product Roadmap Review",
			"2026-02-15 Backend Guild - Kafka Setup",
			"2026-02-12 Mobile Team - UI Refresh",
			"2026-02-10 Stakeholder Update - Q1 Progress",
			"2026-02-08 Architecture Review - Database Choice",
			"2026-02-05 Security Committee Meeting",
			"2026-02-02 Frontend Guild - React Migration",
			"2026-01-30 SRE Sync - Error Budget Update",
			"2026-01-28 Growth Team - Experiment Results",
		},
		"Architecture Decisions": {
			"ADR: Selecting Go for High-Performance Services",
			"ADR: Adopting GraphQL for Mobile Frontend",
			"ADR: Migrating to PostgreSQL for Primary Data",
			"ADR: Use of Service Mesh for Inter-service Auth",
			"ADR: Implementation of Event-Driven Billing",
			"ADR: Standardizing on OpenTelemetry for Tracing",
			"ADR: Choice of Vector Database for AI Search",
			"ADR: Moving to Monorepo for Shared Components",
			"ADR: Selection of Managed Kubernetes Service",
			"ADR: Strategy for Zero-Downtime Deployments",
		},
		"Sprint Documentation": {
			"Sprint 45 Retrospective - Outcomes",
			"Sprint 46 Planning - Velocity and Goals",
			"Sprint 44 Demo - Key Features Shown",
			"Sprint 47 Backlog Refinement Notes",
			"Sprint 45 Capacity and Allocation",
			"Engineering Velocity Dashboard Report",
			"QA Regression Suite Status - Sprint 45",
			"Release Notes v2.4.0",
			"Post-Launch Support Plan - SSO Feature",
			"Beta Testing Feedback Summary",
		},
		"Strategy & Proposals": {
			"Proposal: AI-Powered Customer Support Integration",
			"Proposal: Edge Computing for Asset Delivery",
			"Proposal: Unified Analytics Platform",
			"Proposal: Technical Debt Reduction Initiative",
			"Proposal: Developer Experience (DX) Improvements",
			"Proposal: Multi-Region Deployment for Latency",
			"Proposal: Serverless Functions for Async Tasks",
			"Proposal: Internal API Catalog Tooling",
			"Proposal: Automated Dependency Updating",
			"Proposal: Performance Benchmarking Suite",
		},
	}
}

func generateSpace(dir, spaceKey, spaceName string, categories map[string][]string) {
	fmt.Printf("Generating pages for %s in %s...\n", spaceKey, dir)

	os.MkdirAll(dir, 0755)
	assetsDir := filepath.Join(dir, "assets")
	os.MkdirAll(assetsDir, 0755)

	os.WriteFile(filepath.Join(assetsDir, "system_architecture_overview.png"), []byte("dummy image content"), 0644)
	os.WriteFile(filepath.Join(assetsDir, "product_roadmap_2026.pdf"), []byte("dummy pdf content"), 0644)

	// Home page
	homePage := filepath.Join(dir, spaceName+".md")

	var links string
	if spaceKey == tdSpace {
		links = "- [Architecture Deep Dive](./Core-Systems/Authentication-Service-Deep-Dive.md)\n- [Security Plan](./Security/Zero-Trust-Network-Implementation.md)"
	} else {
		links = "- [SSO PRD](./Product-Requirements/PRD-Enterprise-Single-Sign-On-SSO.md)\n- [Recent ADRs](./Architecture-Decisions/ADR-Selecting-Go-for-High-Performance-Services.md)"
	}

	homeContent := fmt.Sprintf(`---
space: %s
title: "%s Home"
---

# %s Hub

Welcome to the central documentation hub for %s.

## Featured Sections
%s

## Recent Activity
- Sprint 46 planning completed.
- Security audit for Q1 is in progress.
- API documentation updated for v2.4.0.

---
*Created for test comparison.*
`, spaceKey, spaceName, spaceName, spaceName, links)
	os.WriteFile(homePage, []byte(homeContent), 0644)

	count := 0
	for category, titles := range categories {
		catDirName := strings.ReplaceAll(category, " ", "-")
		catPath := filepath.Join(dir, catDirName)
		os.MkdirAll(catPath, 0755)

		for _, title := range titles {
			writeHighQualityPage(dir, spaceKey, catDirName, title)
			count++
		}

		// Add more realistic variations to get to 20+ per category (100+ per space)
		for i := 1; i <= 12; i++ {
			subTitle := fmt.Sprintf("%s - Deep Dive Part %d", titles[i%len(titles)], i)
			writeHighQualityPage(dir, spaceKey, catDirName, subTitle)
			count++
		}
	}
	fmt.Printf("Generated %d unique pages for %s\n", count, spaceKey)
}

func sanitizeTitle(t string) string {
	t = strings.ReplaceAll(t, ":", "")
	t = strings.ReplaceAll(t, "/", "-")
	t = strings.ReplaceAll(t, " (", "-")
	t = strings.ReplaceAll(t, ")", "")
	t = strings.ReplaceAll(t, " ", "-")
	return t
}

func writeHighQualityPage(dir, spaceKey, catDir, title string) {
	fileName := sanitizeTitle(title) + ".md"
	filePath := filepath.Join(dir, catDir, fileName)

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("space: %s\n", spaceKey))
	sb.WriteString(fmt.Sprintf("title: %q\n", title))
	sb.WriteString("---\n\n")
	sb.WriteString(fmt.Sprintf("# %s\n\n", title))

	// Determine content style
	if strings.HasPrefix(title, "ADR") {
		sb.WriteString("## Status\nAccepted\n\n## Context\nOur current implementation of this system faces significant scalability challenges. As we grow our user base, the latency for critical operations has increased by 40%.\n\n## Decision\nWe will implement a distributed architecture using modern patterns and reliable tooling. This choice balances developer productivity with long-term maintenance costs.\n\n## Consequences\n- Better horizontal scaling.\n- More complex deployment pipeline.\n- Consistent performance under load.\n")
	} else if strings.Contains(title, "PRD") {
		sb.WriteString("## Objective\nTo provide a seamless and secure experience for our users, specifically targeting the improvements needed for " + title + ".\n\n")
		sb.WriteString("## User Stories\n1. As a user, I want to securely access my data from any device.\n2. As an admin, I want to audit user activity to ensure compliance.\n\n")
		sb.WriteString("## Acceptance Criteria\n- Response time < 200ms.\n- Zero data loss during transition.\n- 100% test coverage for core flows.\n")
	} else if strings.Contains(title, "Sync") || strings.Contains(title, "Meeting") {
		sb.WriteString("## Attendance\n- Engineering Leads\n- Product Management\n- Infrastructure Team\n\n")
		sb.WriteString("## Discussion Summary\nWe reviewed the progress on the current milestones. Most items are on track, but we identified a risk in the database migration plan. The team decided to perform a dry-run in the staging environment next Tuesday.\n\n")
		sb.WriteString("## Action Items\n- [ ] Schedule staging dry-run (@ops)\n- [ ] Refine migration script (@dev-team)\n- [ ] Notify stakeholders of potential downtime (@pm)\n")
	} else if strings.Contains(title, "Architecture") || strings.Contains(title, "System") {
		sb.WriteString("## Overview\nThis document provides the high-level design and data flow patterns for the components involved in " + title + ".\n\n")
		sb.WriteString("## Visual Diagram\n```mermaid\ngraph TD\n  Client[Application] --> API[Gateway Service]\n  API --> Logic[Business Logic Cluster]\n  Logic --> Persistence[(Distributed Database)]\n  Logic --> Cache((Cache Layer))\n```\n\n")
		sb.WriteString("## Component Responsibilities\n- **Gateway**: Handles TLS termination and JWT verification.\n- **Logic**: Implements domain rules and coordinates between services.\n- **Persistence**: Ensures high availability and data durability.\n")
	} else {
		sb.WriteString("## Introduction\nThis page provides detailed technical documentation for " + title + ". It is part of the " + catDir + " series.\n\n")
		sb.WriteString("## Details\nThe system has been designed with reliability and performance as top priorities. We utilize a combination of synchronous APIs for real-time needs and asynchronous processing for background tasks. This allows us to maintain a responsive user interface even during heavy background processing loads.\n")
	}

	// Add random resource references
	if rand.Intn(4) == 0 {
		sb.WriteString("\n## Resources\n")
		sb.WriteString("![Architecture Diagram](../assets/system_architecture_overview.png)\n")
		sb.WriteString("Refer to the [Product Roadmap 2026](../assets/product_roadmap_2026.pdf) for context.\n")
	}

	sb.WriteString("\n\n---\n*Technical Documentation - Company Confidential*")

	os.WriteFile(filePath, []byte(sb.String()), 0644)
}
