package durchunk

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkChunks verifies that every key is assigned exactly once and totals are consistent.
func checkChunks(t *testing.T, data map[string]time.Duration, chunks []Chunk[string]) {
	t.Helper()
	keySet := make(map[string]bool)
	var total time.Duration
	for _, c := range chunks {
		var sum time.Duration
		for _, k := range c.Keys {
			assert.False(t, keySet[k], "duplicate key: %s", k)
			keySet[k] = true
			sum += data[k]
		}
		assert.Equal(t, sum, c.Total, "chunk total mismatch")
		total += c.Total
	}
	var want time.Duration
	for _, v := range data {
		want += v
	}
	assert.Equal(t, want, total, "total duration mismatch")
	assert.Equal(t, len(data), len(keySet), "not all keys assigned")
}

func spread(chunks []Chunk[string]) (lo, hi time.Duration) {
	lo, hi = chunks[0].Total, chunks[0].Total
	for _, c := range chunks[1:] {
		lo, hi = min(lo, c.Total), max(hi, c.Total)
	}
	return lo, hi
}

func TestSplitBalanced_ManyChunks(t *testing.T) {
	data := map[string]time.Duration{
		"a": 3 * time.Second,
		"b": 2 * time.Second,
		"c": 1 * time.Second,
		"d": 4 * time.Second,
		"e": 5 * time.Second,
		"f": 6 * time.Second,
		"g": 7 * time.Second,
		"h": 8 * time.Second,
		"i": 9 * time.Second,
		"j": 10 * time.Second,
	}
	chunks := SplitBalanced(maps.All(data), 5)
	require.Len(t, chunks, 5)
	checkChunks(t, data, chunks)

	// Total 55s / 5 = 11s, perfectly balanced split exists
	lo, hi := spread(chunks)
	assert.Equal(t, 11*time.Second, hi, "makespan")
	assert.Equal(t, 11*time.Second, lo)
}

func TestSplitBalanced_Basic(t *testing.T) {
	data := map[string]time.Duration{
		"a": 3 * time.Second,
		"b": 2 * time.Second,
		"c": 1 * time.Second,
		"d": 4 * time.Second,
	}
	chunks := SplitBalanced(maps.All(data), 2)
	require.Len(t, chunks, 2)
	checkChunks(t, data, chunks)
	_, hi := spread(chunks)
	assert.Equal(t, 5*time.Second, hi)
}

func TestSplitBalanced_SubSecond(t *testing.T) {
	// Durations below 1s must not be truncated to zero
	data := map[string]time.Duration{}
	for i := range 20 {
		data[fmt.Sprintf("k%02d", i)] = time.Duration(i+1) * 10 * time.Millisecond
	}
	chunks := SplitBalanced(maps.All(data), 3)
	require.Len(t, chunks, 3)
	checkChunks(t, data, chunks)

	lo, hi := spread(chunks)
	assert.LessOrEqual(t, hi-lo, 10*time.Millisecond, "lo=%v hi=%v", lo, hi)
}

func TestSplitBalanced_Deterministic(t *testing.T) {
	keys := make([]string, 200)
	durs := make([]time.Duration, 200)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%03d", i)
		durs[i] = time.Duration((i*7919)%1000+1) * time.Millisecond
	}
	seq := func(yield func(string, time.Duration) bool) {
		for i := range keys {
			if !yield(keys[i], durs[i]) {
				return
			}
		}
	}
	first := SplitBalanced(seq, 4)
	for range 3 {
		assert.Equal(t, first, SplitBalanced(seq, 4))
	}
	// Keys in each chunk keep the input order
	for _, c := range first {
		assert.True(t, slices.IsSorted(c.Keys))
	}
}

func TestSplitBalanced_OneChunk(t *testing.T) {
	data := map[string]time.Duration{
		"a": 1 * time.Second,
		"b": 2 * time.Second,
	}
	chunks := SplitBalanced(maps.All(data), 1)
	require.Len(t, chunks, 1)
	assert.ElementsMatch(t, []string{"a", "b"}, chunks[0].Keys)
	assert.Equal(t, 3*time.Second, chunks[0].Total)
}

func TestSplitBalanced_ChunkCountExceedsKeys(t *testing.T) {
	data := map[string]time.Duration{
		"a": 1 * time.Second,
		"b": 2 * time.Second,
	}
	chunks := SplitBalanced(maps.All(data), 3)
	require.Len(t, chunks, 3)
	checkChunks(t, data, chunks)
}

func TestSplitBalanced_ZeroDurations(t *testing.T) {
	data := map[string]time.Duration{"a": 0, "b": 0, "c": 0}
	chunks := SplitBalanced(maps.All(data), 2)
	require.Len(t, chunks, 2)
	checkChunks(t, data, chunks)
}

func TestSplitBalanced_InvalidChunkCount(t *testing.T) {
	data := map[string]time.Duration{"a": time.Second}
	assert.Nil(t, SplitBalanced(maps.All(data), 0))
	assert.Nil(t, SplitBalanced(maps.All(data), -1))
}
