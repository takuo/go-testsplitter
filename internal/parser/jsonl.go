// Package parser provides parsers for test results.
package parser

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/takuo/go-testsplitter/internal/types"
)

type testEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Elapsed float64   `json:"Elapsed"` // seconds
}

// Result is a measured duration.
type Result struct {
	Duration time.Duration
	// Time is when the result was recorded. Zero if unknown.
	Time time.Time
}

// Results is the parsed results of `go test -json` output.
type Results struct {
	// Tests is the duration of top-level tests. If a test appears more than once (e.g. rerun), the last result wins.
	Tests map[types.TestKey]Result
}

// ParseGoTestJSONL parses `go test -json` output (JSON Lines). Subtests are ignored.
// Lines that are not valid JSON are skipped.
func ParseGoTestJSONL(r io.Reader) (*Results, error) {
	p := &jsonlParser{
		results: &Results{Tests: make(map[types.TestKey]Result)},
		starts:  make(map[types.TestKey]time.Time),
	}

	br := bufio.NewReader(r)
	for lineNo := 1; ; lineNo++ {
		line, err := br.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return p.results, fmt.Errorf("failed to read line %d: %w", lineNo, err)
		}
		if line = bytes.TrimSpace(line); len(line) > 0 {
			p.parseLine(line, lineNo)
		}
		if err != nil { // io.EOF
			return p.results, nil
		}
	}
}

type jsonlParser struct {
	results *Results
	starts  map[types.TestKey]time.Time
}

func (p *jsonlParser) parseLine(line []byte, lineNo int) {
	var ev testEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		slog.Warn("Skipping invalid JSON line", "line", lineNo, "err", err)
		return
	}
	if ev.Test == "" || strings.Contains(ev.Test, "/") {
		return // ignore package-level events and subtests
	}
	if !isResult(ev.Action) && ev.Action != "run" {
		return
	}

	key := types.TestKey{Package: ev.Package, Function: ev.Test}
	if ev.Action == "run" {
		p.starts[key] = ev.Time
		return
	}
	var d time.Duration
	switch {
	case ev.Elapsed > 0:
		d = seconds(ev.Elapsed)
	case !p.starts[key].IsZero() && !ev.Time.IsZero():
		d = ev.Time.Sub(p.starts[key])
	}
	p.results.Tests[key] = Result{Duration: d, Time: ev.Time}
}

func isResult(action string) bool {
	return action == "pass" || action == "fail" || action == "skip"
}

func seconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}
