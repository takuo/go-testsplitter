package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gotest.tools/v3/golden"
)

func TestMainIntegration(t *testing.T) {
	const nodes = 2

	cur, err := os.Getwd()
	require.NoError(t, err, "Should be able to get current directory")

	testdataDir := filepath.Join(cur, "testdata")
	// t.Chdir(testdataDir)

	// Build the testsplitter binary
	binary := filepath.Join(cur, "testsplitter")
	cmd := exec.Command("go", "build", "-o", binary, filepath.Join(cur, "main.go"))
	cmd.Dir = cur
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build testsplitter: %v", err)
	}
	defer os.Remove(binary)

	// Prepare test input
	input := "example/pkg1\nexample/pkg2\nexample/pkg3"
	outputDir := t.TempDir()
	goldenDir := filepath.Join(cur, "testdata", "golden")

	// Run testsplitter
	cmd = exec.Command(binary, "-d", "-n", strconv.Itoa(nodes), "-o", outputDir, "--", "-test.timeout=20m", "-test.v")
	cmd.Stdin = strings.NewReader(input)
	cmd.Dir = testdataDir

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "testsplitter should run successfully. Output: %s", output)

	// Check that output files were created
	for i := range nodes {
		outputFile := filepath.Join(outputDir, "test-node-"+strconv.Itoa(i)+".sh")
		goldenFile := filepath.Join(goldenDir, "test-node-"+strconv.Itoa(i)+".sh.golden")
		assert.FileExists(t, outputFile, "Output file should be created")
		b, err := os.ReadFile(outputFile)
		require.NoError(t, err)
		golden.Assert(t, string(b), goldenFile)
	}
}

func TestWithPreviousResults(t *testing.T) {
	const nodes = 3

	cur, err := os.Getwd()
	require.NoError(t, err, "Should be able to get current directory")

	testdataDir := filepath.Join(cur, "testdata")

	// Build the testsplitter binary
	binary := filepath.Join(cur, "testsplitter")
	cmd := exec.Command("go", "build", "-o", binary, filepath.Join(cur, "main.go"))
	cmd.Dir = cur
	err = cmd.Run()
	require.NoError(t, err, "Should be able to build testsplitter")
	defer os.Remove(binary)

	// Prepare test input (using testdata paths)
	input := "example/pkg1\nexample/pkg2\nexample/pkg3"
	outputDir := t.TempDir()

	// Run testsplitter with test-reports available
	cmd = exec.Command(binary, "-d", "-n", strconv.Itoa(nodes), "-o", outputDir, "--", "-test.timeout=30m", "-test.v")
	cmd.Stdin = strings.NewReader(input)
	cmd.Dir = testdataDir

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "testsplitter should run successfully. Output: %s", output)
	assert.Contains(t, string(output), "Loaded 12 testcases durations from 3 files in ./test-reports", "Should load previous test durations")
	t.Log(string(output))

	// Check that output files were created
	for i := range nodes {
		outputFile := filepath.Join(outputDir, "test-node-"+strconv.Itoa(i)+".sh")
		assert.FileExists(t, outputFile, "Output file should be created")

		// Check that the file contains expected content
		content, err := os.ReadFile(outputFile)
		require.NoError(t, err, "Should be able to read output file")

		contentStr := string(content)
		assert.Contains(t, contentStr, "#!/bin/bash", "File should contain bash shebang")
		assert.Contains(t, contentStr, "set -e", "File should contain set -e")
	}
}
