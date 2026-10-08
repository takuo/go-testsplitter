// Package parser provides parsers for test results.
package parser

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
)

type testEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Elapsed float64   `json:"Elapsed"` // seconds
}

// ParseGoTestJSONL parses `go test -json` output (JSON Lines) and returns durations of top-level tests.
// Subtests are ignored. Lines that are not valid JSON are skipped.
// If a test appears more than once (e.g. rerun), the last result wins.
func ParseGoTestJSONL(r io.Reader) (map[string]time.Duration, error) {
	starts := make(map[string]time.Time)
	results := make(map[string]time.Duration)

	br := bufio.NewReader(r)
	for lineNo := 1; ; lineNo++ {
		line, err := br.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return results, fmt.Errorf("failed to read line %d: %w", lineNo, err)
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			parseLine(line, lineNo, starts, results)
		}
		if err != nil { // io.EOF
			return results, nil
		}
	}
}

func parseLine(line []byte, lineNo int, starts map[string]time.Time, results map[string]time.Duration) {
	var ev testEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		log.Printf("Skipping invalid JSON at line %d: %v", lineNo, err)
		return
	}
	if ev.Test == "" || strings.Contains(ev.Test, "/") {
		// ignore package-level events and subtests
		return
	}
	key := ev.Package + ":" + ev.Test

	switch ev.Action {
	case "run":
		starts[key] = ev.Time
	case "pass", "fail", "skip":
		switch {
		case ev.Elapsed > 0:
			results[key] = time.Duration(ev.Elapsed * float64(time.Second))
		case !starts[key].IsZero() && !ev.Time.IsZero():
			results[key] = ev.Time.Sub(starts[key])
		default:
			results[key] = 0
		}
	}
}
