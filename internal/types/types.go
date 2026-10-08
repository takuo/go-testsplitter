// Package types provides types shared between testsplitter packages.
package types

import (
	"encoding/xml"
	"time"
)

// TestSuite represents a JUnit XML test suite
type TestSuite struct {
	XMLName   xml.Name   `xml:"testsuite"`
	Name      string     `xml:"name,attr"`
	Tests     int        `xml:"tests,attr"`
	Failures  int        `xml:"failures,attr"`
	Errors    int        `xml:"errors,attr"`
	Time      float64    `xml:"time,attr"`
	TestCases []TestCase `xml:"testcase"`
}

// TestCase represents a JUnit XML test case
type TestCase struct {
	XMLName   xml.Name `xml:"testcase"`
	Name      string   `xml:"name,attr"`
	Classname string   `xml:"classname,attr"`
	Time      float64  `xml:"time,attr"`
	Failure   *Failure `xml:"failure,omitempty"`
	Error     *Error   `xml:"error,omitempty"`
}

// Failure represents a JUnit XML test failure
type Failure struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// Error represents a JUnit XML test error
type Error struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// TestKey identifies a top-level test function in a package.
type TestKey struct {
	Package  string
	Function string
}

// TestInfo holds information about a test function
type TestInfo struct {
	TestKey
	Duration time.Duration
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
	Index       int
	Package     string
	TestPattern string
}
