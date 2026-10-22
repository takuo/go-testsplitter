// Package archive compacts `go test -json` results into the events needed to split tests,
// so that previous results can be kept across many runs without growing without bound.
package archive

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Stats is the result of Compact.
type Stats struct {
	// Kept and Dropped are the numbers of events written and skipped.
	Kept, Dropped int
	// BytesIn and BytesOut are the sizes read and written.
	BytesIn, BytesOut int64
}

// event is the part of a `go test -json` event needed to decide whether to keep it.
type event struct {
	Action string `json:"Action"`
	Test   string `json:"Test"`
}

// Compact copies the events needed to split tests from r to w, one JSON event per line:
// the results (pass, fail, skip) of packages and of top-level tests, which carry the elapsed
// times. Output events, which are the bulk of the results, subtests and events without timing
// are dropped. Kept events are copied as they are, so the result is still `go test -json` output.
func Compact(w io.Writer, r io.Reader) (Stats, error) {
	var st Stats
	br := bufio.NewReader(r)
	bw := bufio.NewWriter(w)

	for lineNo := 1; ; lineNo++ {
		line, err := br.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return st, fmt.Errorf("failed to read line %d: %w", lineNo, err)
		}
		st.BytesIn += int64(len(line))
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			switch {
			case !keep(trimmed, lineNo):
				st.Dropped++
			default:
				st.Kept++
				if _, werr := bw.Write(trimmed); werr != nil {
					return st, werr
				}
				if werr := bw.WriteByte('\n'); werr != nil {
					return st, werr
				}
				st.BytesOut += int64(len(trimmed)) + 1
			}
		}
		if err != nil { // io.EOF
			break
		}
	}
	if err := bw.Flush(); err != nil {
		return st, err
	}
	return st, nil
}

// CopyAll copies r to w unchanged, counting the bytes.
func CopyAll(w io.Writer, r io.Reader) (Stats, error) {
	n, err := io.Copy(w, r)
	return Stats{BytesIn: n, BytesOut: n}, err
}

// keep reports whether the event is a result of a package or of a top-level test.
func keep(line []byte, lineNo int) bool {
	var ev event
	if err := json.Unmarshal(line, &ev); err != nil {
		slog.Debug("Dropping invalid JSON line", "line", lineNo, "err", err)
		return false
	}
	switch ev.Action {
	case "pass", "fail", "skip":
		return !strings.Contains(ev.Test, "/") // subtests are ignored when splitting
	}
	return false
}
