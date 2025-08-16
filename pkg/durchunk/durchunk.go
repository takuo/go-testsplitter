// Package durchunk provides functions to split a map of key-durations into balanced chunks.
package durchunk

import (
	"iter"
	"math"
	"math/rand"
	"time"
)

// Chunk chunk after balanced split
type Chunk struct {
	Keys  []string      `json:"keys"`
	Total time.Duration `json:"total_seconds"`
}

type entry struct {
	Key string
	Dur int64
}

// SplitBalanced は map[string]time.Duration を指定したチャンク数に分割します。
// - 合計時間を均等化
// - 要素数に制約なし（最低1個以上）
func SplitBalanced(data iter.Seq2[string, time.Duration], chunkCount int) []Chunk {
	entries := []entry{}
	globalDurMap := make(map[string]int64)
	for k, v := range data {
		sec := int64(v.Seconds())
		globalDurMap[k] = sec
		entries = append(entries, entry{Key: k, Dur: sec})
	}

	chunks := greedyPartition(entries, chunkCount)
	chunks = simulatedAnnealing(chunks, 50000, 1000.0, 0.01, globalDurMap)

	for i := range chunks {
		totalSec := int64(0)
		for _, k := range chunks[i].Keys {
			totalSec += globalDurMap[k]
		}
		chunks[i].Total = time.Duration(totalSec) * time.Second
	}

	return chunks
}

// --------------------
// 内部関数
// --------------------
func greedyPartition(entries []entry, m int) []Chunk {
	rand.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })

	chunks := make([]Chunk, m)
	sums := make([]int64, m)
	for _, e := range entries {
		best := 0
		for i := 1; i < m; i++ {
			if sums[i] < sums[best] {
				best = i
			}
		}
		chunks[best].Keys = append(chunks[best].Keys, e.Key)
		sums[best] += e.Dur
		chunks[best].Total = time.Duration(sums[best]) * time.Second
	}
	return chunks
}

func simulatedAnnealing(chunks []Chunk, iterations int, tempStart, tempEnd float64, durMap map[string]int64) []Chunk {
	best := copyChunks(chunks)
	bestScore := score(best)
	current := copyChunks(chunks)
	currentScore := bestScore

	for i := range iterations {
		t := tempStart * math.Pow(tempEnd/tempStart, float64(i)/float64(iterations))
		next := copyChunks(current)

		if rand.Float64() < 0.5 {
			from := rand.Intn(len(next))
			if len(next[from].Keys) == 0 {
				continue
			}
			to := rand.Intn(len(next))
			if from == to {
				continue
			}
			idx := rand.Intn(len(next[from].Keys))
			val := next[from].Keys[idx]
			next[from].Keys = append(next[from].Keys[:idx], next[from].Keys[idx+1:]...)
			next[to].Keys = append(next[to].Keys, val)
		} else {
			a := rand.Intn(len(next))
			b := rand.Intn(len(next))
			if a == b || len(next[a].Keys) == 0 || len(next[b].Keys) == 0 {
				continue
			}
			ia := rand.Intn(len(next[a].Keys))
			ib := rand.Intn(len(next[b].Keys))
			next[a].Keys[ia], next[b].Keys[ib] = next[b].Keys[ib], next[a].Keys[ia]
		}

		for i := range next {
			sum := int64(0)
			for _, k := range next[i].Keys {
				sum += durMap[k]
			}
			next[i].Total = time.Duration(sum) * time.Second
		}

		nextScore := score(next)
		delta := float64(nextScore - currentScore)
		if delta < 0 || rand.Float64() < math.Exp(-delta/t) {
			current = next
			currentScore = nextScore
		}
		if currentScore < bestScore {
			best = copyChunks(current)
			bestScore = currentScore
		}
	}

	return best
}

func score(chunks []Chunk) int64 {
	min, max := chunks[0].Total.Seconds(), chunks[0].Total.Seconds()
	for _, c := range chunks[1:] {
		sec := c.Total.Seconds()
		if sec < min {
			min = sec
		}
		if sec > max {
			max = sec
		}
	}
	return int64(max - min)
}

func copyChunks(chunks []Chunk) []Chunk {
	newChunks := make([]Chunk, len(chunks))
	for i := range chunks {
		keys := make([]string, len(chunks[i].Keys))
		copy(keys, chunks[i].Keys)
		newChunks[i] = Chunk{
			Keys:  keys,
			Total: chunks[i].Total,
		}
	}
	return newChunks
}
