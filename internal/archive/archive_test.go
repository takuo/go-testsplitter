package archive

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompact(t *testing.T) {
	long := strings.Repeat("x", 128*1024) // longer than the default bufio.Scanner limit
	input := strings.Join([]string{
		`{"Time":"2026-10-01T00:00:00Z","Action":"start","Package":"p"}`,
		`{"Time":"2026-10-01T00:00:00Z","Action":"run","Package":"p","Test":"TestA"}`,
		`{"Time":"2026-10-01T00:00:00Z","Action":"output","Package":"p","Test":"TestA","Output":"` + long + `"}`,
		`{"Time":"2026-10-01T00:00:01Z","Action":"pass","Package":"p","Test":"TestA/sub","Elapsed":0.5}`,
		`{"Time":"2026-10-01T00:00:02Z","Action":"pass","Package":"p","Test":"TestA","Elapsed":1}`,
		`{"Time":"2026-10-01T00:00:03Z","Action":"fail","Package":"p","Test":"TestB","Elapsed":2}`,
		`{"Time":"2026-10-01T00:00:04Z","Action":"skip","Package":"p","Test":"TestC","Elapsed":0}`,
		"",
		"  ",
		"not json",
		`{"Time":"2026-10-01T00:00:05Z","Action":"fail","Package":"p","Elapsed":4}`,
	}, "\n") // no trailing newline

	var out strings.Builder
	st, err := Compact(&out, strings.NewReader(input))
	require.NoError(t, err)

	assert.Equal(t, []string{
		`{"Time":"2026-10-01T00:00:02Z","Action":"pass","Package":"p","Test":"TestA","Elapsed":1}`,
		`{"Time":"2026-10-01T00:00:03Z","Action":"fail","Package":"p","Test":"TestB","Elapsed":2}`,
		`{"Time":"2026-10-01T00:00:04Z","Action":"skip","Package":"p","Test":"TestC","Elapsed":0}`,
		`{"Time":"2026-10-01T00:00:05Z","Action":"fail","Package":"p","Elapsed":4}`,
		"",
	}, strings.Split(out.String(), "\n"))

	assert.Equal(t, 4, st.Kept)
	assert.Equal(t, 5, st.Dropped) // start, run, output, subtest result and the invalid line
	assert.Equal(t, int64(len(input)), st.BytesIn)
	assert.Equal(t, int64(out.Len()), st.BytesOut)
	assert.Less(t, st.BytesOut, st.BytesIn/100)
}

func TestCompact_Empty(t *testing.T) {
	var out strings.Builder
	st, err := Compact(&out, strings.NewReader(""))
	require.NoError(t, err)
	assert.Empty(t, out.String())
	assert.Equal(t, Stats{}, st)
}

func TestCopyAll(t *testing.T) {
	input := "{\"Action\":\"output\"}\nnot json\n"
	var out strings.Builder
	st, err := CopyAll(&out, strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, input, out.String())
	assert.Equal(t, Stats{BytesIn: int64(len(input)), BytesOut: int64(len(input))}, st)
}
