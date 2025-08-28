package types

import (
	"encoding/xml"
	"iter"
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

// TestInfo holds information about a test function
type TestInfo struct {
	Package  string
	Function string
	Duration time.Duration
}

// NodeTest represents a test assigned to a specific node
type NodeTest struct {
	NodeIndex     int
	TotalDuration time.Duration
	Funcs         map[string][]string
	Flags         string
}

// TemplateData represents data for the script template
type TemplateData struct {
	NodeIndex   int
	Concurrency int
	TestLines   iter.Seq[TestLine]
	JSONDir     string
	BinariesDir string
	Flags       string
}

// TestLine represents a single line in the test script
type TestLine struct {
	Package     string
	TestPattern string
	Flags       string
}
