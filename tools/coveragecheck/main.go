package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type coverageGate struct {
	pkg      string
	minimum  float64
	profile  string
	reported float64
}

func main() {
	gates := []coverageGate{
		{pkg: "./cmd", minimum: 65.0, profile: "coverage-cmd.out"},
		{pkg: "./internal/sync", minimum: 65.0, profile: "coverage-sync.out"},
		{pkg: "./internal/git", minimum: 55.0, profile: "coverage-git.out"},
	}

	allPassed := true
	for i := range gates {
		coverage, err := testAndMeasureCoverage(gates[i].pkg, gates[i].profile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "coverage check failed for %s: %v\n", gates[i].pkg, err)
			allPassed = false
			continue
		}
		gates[i].reported = coverage

		status := "PASS"
		if coverage < gates[i].minimum {
			status = "FAIL"
			allPassed = false
		}
		fmt.Printf("%s %s coverage: %.1f%% (minimum %.1f%%)\n", status, gates[i].pkg, coverage, gates[i].minimum)
	}

	for _, gate := range gates {
		if gate.profile == "" {
			continue
		}
		_ = os.Remove(gate.profile)
	}

	if !allPassed {
		os.Exit(1)
	}
}

func testAndMeasureCoverage(pkg, profile string) (float64, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return 0, fmt.Errorf("profile is required")
	}

	profilePath := filepath.Clean(profile)
	if err := runCommand("go", "test", "-covermode=atomic", "-coverprofile="+profilePath, pkg); err != nil {
		return 0, err
	}

	out, err := runCommandOutput("go", "tool", "cover", "-func="+profilePath)
	if err != nil {
		return 0, err
	}

	coverage, err := parseTotalCoverage(out)
	if err != nil {
		return 0, err
	}
	return coverage, nil
}

func parseTotalCoverage(out string) (float64, error) {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "total:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		raw := fields[len(fields)-1]
		raw = strings.TrimSpace(strings.TrimSuffix(raw, "%"))
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, fmt.Errorf("parse total coverage %q: %w", raw, err)
		}
		return value, nil
	}
	return 0, fmt.Errorf("total coverage line not found")
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...) //nolint:gosec // Intended use of arbitrary commands for tooling
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCommandOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...) //nolint:gosec // Intended use of arbitrary commands for tooling
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s %s failed: %s", name, strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}
