package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	basePath := "D:/confluence-test-data"
	filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".md") {
			cleanFile(path)
		}
		return nil
	})
}

func cleanFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "## References") || strings.Contains(line, "## Quick Links") {
			break
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return
	}

	os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
	fmt.Printf("Cleaned %s\n", path)
}
