// Package types provides types shared between testsplitter packages.
package types

import (
	"time"
)

// TestKey identifies a top-level test function in a package.
type TestKey struct {
	Package  string
	Function string
}

// TestInfo holds information about a test function
type TestInfo struct {
	TestKey
	Duration time.Duration
	// Known reports whether Duration comes from previous results.
	Known bool
}

// NodeTest represents tests assigned to a specific node
type NodeTest struct {
	NodeIndex     int
	TotalDuration time.Duration
	// Packages is the list of packages in deterministic order.
	Packages []string
	// Funcs maps package to its test functions.
	Funcs map[string][]string
}

// TemplateData represents data for the script template
type TemplateData struct {
	NodeIndex   int
	Concurrency int
	TestLines   []TestLine
	JSONDir     string
	BinariesDir string
	// Flags is the test flags joined with spaces (not shell-quoted).
	Flags string
	// TestFlags is the raw list of test flags. Use with the shquote template function.
	TestFlags []string
}

// TestLine represents a single test process invocation in the test script
type TestLine struct {
	// Index is the 1-origin sequence number in the node.
	Index   int
	Package string
	// Binary is the file name of the test binary in BinariesDir.
	Binary      string
	TestPattern string
	// Functions is the test functions matched by TestPattern.
	Functions []string
}
