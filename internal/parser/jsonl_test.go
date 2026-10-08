package parser

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/takuo/go-testsplitter/internal/types"
)

func key(pkg, fn string) types.TestKey {
	return types.TestKey{Package: pkg, Function: fn}
}

func durations(r *Results) map[types.TestKey]time.Duration {
	m := make(map[types.TestKey]time.Duration, len(r.Tests))
	for k, v := range r.Tests {
		m[k] = v.Duration
	}
	return m
}

func TestParseGoTestJSONL(t *testing.T) {
	input := `{"Time":"2023-01-01T00:00:00Z","Action":"start","Package":"pkg"}
{"Time":"2023-01-01T00:00:00Z","Action":"run","Package":"pkg","Test":"TestA"}
{"Time":"2023-01-01T00:00:00.1Z","Action":"run","Package":"pkg","Test":"TestA/sub"}
{"Time":"2023-01-01T00:00:00.2Z","Action":"output","Package":"pkg","Test":"TestA","Output":"hello\n"}
{"Time":"2023-01-01T00:00:00.3Z","Action":"pass","Package":"pkg","Test":"TestA/sub","Elapsed":0.2}
{"Time":"2023-01-01T00:00:01.5Z","Action":"pass","Package":"pkg","Test":"TestA","Elapsed":1.5}
not a json line
{"Time":"2023-01-01T00:00:02Z","Action":"run","Package":"pkg","Test":"TestB"}
{"Time":"2023-01-01T00:00:02.25Z","Action":"fail","Package":"pkg","Test":"TestB"}
{"Time":"2023-01-01T00:00:03Z","Action":"run","Package":"pkg","Test":"TestFast"}
{"Time":"2023-01-01T00:00:03Z","Action":"pass","Package":"pkg","Test":"TestFast","Elapsed":0}
{"Time":"2023-01-01T00:00:04Z","Action":"run","Package":"pkg","Test":"TestRunning"}
{"Time":"2023-01-01T00:00:05Z","Action":"pass","Package":"pkg","Elapsed":5}`

	got, err := ParseGoTestJSONL(strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, map[types.TestKey]time.Duration{
		key("pkg", "TestA"):    1500 * time.Millisecond, // Elapsed
		key("pkg", "TestB"):    250 * time.Millisecond,  // fallback to Time difference
		key("pkg", "TestFast"): 0,                       // known, but fast
	}, durations(got))
}

func TestParseGoTestJSONL_Rerun(t *testing.T) {
	input := `{"Action":"run","Package":"pkg","Test":"TestA"}
{"Action":"fail","Package":"pkg","Test":"TestA","Elapsed":3}
{"Action":"run","Package":"pkg","Test":"TestA"}
{"Action":"pass","Package":"pkg","Test":"TestA","Elapsed":2}
`
	got, err := ParseGoTestJSONL(strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, map[types.TestKey]time.Duration{key("pkg", "TestA"): 2 * time.Second}, durations(got))
}

func TestParseGoTestJSONL_LongLine(t *testing.T) {
	long := strings.Repeat("x", 1<<20) // exceeds bufio.Scanner default limit (64KB)
	input := `{"Action":"run","Package":"pkg","Test":"TestA"}
{"Action":"output","Package":"pkg","Test":"TestA","Output":"` + long + `"}
{"Action":"pass","Package":"pkg","Test":"TestA","Elapsed":1}
{"Action":"run","Package":"pkg","Test":"TestB"}
{"Action":"pass","Package":"pkg","Test":"TestB","Elapsed":2}`

	got, err := ParseGoTestJSONL(strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, map[types.TestKey]time.Duration{
		key("pkg", "TestA"): 1 * time.Second,
		key("pkg", "TestB"): 2 * time.Second,
	}, durations(got))
}

func TestParseGoTestJSONL_Time(t *testing.T) {
	input := `{"Time":"2023-01-01T00:00:00Z","Action":"run","Package":"pkg","Test":"TestA"}
{"Time":"2023-01-01T00:00:01Z","Action":"pass","Package":"pkg","Test":"TestA","Elapsed":1}`
	got, err := ParseGoTestJSONL(strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, Result{Duration: time.Second, Time: time.Date(2023, 1, 1, 0, 0, 1, 0, time.UTC)}, got.Tests[key("pkg", "TestA")])
}
