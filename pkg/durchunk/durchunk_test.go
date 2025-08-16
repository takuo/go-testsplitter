package durchunk

import (
	"maps"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

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
	chunkCount := 5
	chunks := SplitBalanced(maps.All(data), chunkCount)
	assert.Len(t, chunks, chunkCount, "expected %d chunks", chunkCount)

	total := time.Duration(0)
	for _, v := range data {
		total += v
	}
	totalChunks := time.Duration(0)
	keySet := make(map[string]bool)
	for _, c := range chunks {
		totalChunks += c.Total
		for _, k := range c.Keys {
			assert.False(t, keySet[k], "duplicate key: %s", k)
			keySet[k] = true
		}
	}
	assert.Equal(t, total, totalChunks, "total duration mismatch")
	assert.Equal(t, len(data), len(keySet), "not all keys assigned")

	// 各チャンクのTotalが近似しているか（最大と最小の差が閾値以下か）
	min, max := chunks[0].Total, chunks[0].Total
	for _, c := range chunks[1:] {
		if c.Total < min {
			min = c.Total
		}
		if c.Total > max {
			max = c.Total
		}
	}
	// 許容する最大差（例: 5秒）
	const threshold = 3 * time.Second
	diff := max - min
	assert.LessOrEqual(t, diff, threshold, "chunk total diff too large: %v", diff)
}

func TestSplitBalanced_Basic(t *testing.T) {
	data := map[string]time.Duration{
		"a": 3 * time.Second,
		"b": 2 * time.Second,
		"c": 1 * time.Second,
		"d": 4 * time.Second,
	}
	chunks := SplitBalanced(maps.All(data), 2)
	assert.Len(t, chunks, 2, "expected 2 chunks")

	total := time.Duration(0)
	for _, v := range data {
		total += v
	}
	totalChunks := chunks[0].Total + chunks[1].Total
	assert.Equal(t, total, totalChunks, "total duration mismatch")

	keySet := make(map[string]bool)
	for _, c := range chunks {
		for _, k := range c.Keys {
			assert.False(t, keySet[k], "duplicate key: %s", k)
			keySet[k] = true
		}
	}
	assert.Equal(t, len(data), len(keySet), "not all keys assigned")
}

func TestSplitBalanced_OneChunk(t *testing.T) {
	data := map[string]time.Duration{
		"a": 1 * time.Second,
		"b": 2 * time.Second,
	}
	chunks := SplitBalanced(maps.All(data), 1)
	assert.Len(t, chunks, 1, "expected 1 chunk")
	assert.ElementsMatch(t, []string{"a", "b"}, chunks[0].Keys, "unexpected keys")
}

func TestSplitBalanced_ChunkCountExceedsKeys(t *testing.T) {
	data := map[string]time.Duration{
		"a": 1 * time.Second,
		"b": 2 * time.Second,
	}
	chunks := SplitBalanced(maps.All(data), 3)
	assert.Len(t, chunks, 3, "expected 3 chunks")
	keyCount := 0
	for _, c := range chunks {
		keyCount += len(c.Keys)
	}
	assert.Equal(t, 2, keyCount, "total key count mismatch")
}
