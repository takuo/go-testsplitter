package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type testEvent struct {
	Time    string `json:"Time"`
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
}

type testRecord struct {
	Start time.Time
	End   time.Time
}

// ParseGoTestJSON parse go test -json output from a bufio.Scanner
func ParseGoTestJSON(scanner *bufio.Scanner) map[string]time.Duration {
	records := make(map[string]*testRecord)
	for scanner.Scan() {
		var ev testEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse JSON: %v\n", err)
			continue
		}

		if ev.Test == "" || strings.Contains(ev.Test, "/") {
			// ignore subtest
			continue
		}
		key := fmt.Sprintf("%s:%s", ev.Package, ev.Test)
		rec, ok := records[key]
		if !ok {
			rec = &testRecord{}
			records[key] = rec
		}

		switch ev.Action {
		case "run":
			rec.Start, _ = time.Parse(time.RFC3339, ev.Time)
		case "pass", "fail", "skip":
			rec.End, _ = time.Parse(time.RFC3339, ev.Time)
		}
	}
	results := make(map[string]time.Duration, len(records))
	for k, v := range records {
		if !v.Start.IsZero() && !v.End.IsZero() {
			results[k] = v.End.Sub(v.Start)
		}
	}
	return results
}
