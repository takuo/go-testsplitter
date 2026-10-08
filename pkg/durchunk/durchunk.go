// Package durchunk provides functions to split a map of key-durations into balanced chunks.
package durchunk

import (
	"iter"
	"math"
	"math/rand/v2"
	"time"
)

// DefaultSeed is the random seed used when WithSeed is not given.
const DefaultSeed uint64 = 1

type options struct {
	seed uint64
}

// Option configures SplitBalanced.
type Option func(*options)

// WithSeed sets the random seed. The same input and seed always produce the same result.
func WithSeed(seed uint64) Option {
	return func(o *options) { o.seed = seed }
}

// Chunk chunk after balanced split
type Chunk struct {
	Keys  []string      `json:"keys"`
	Total time.Duration `json:"total_seconds"`
}

type entry struct {
	Key string
	Dur time.Duration
}

// SplitBalanced は map[string]time.Duration を指定したチャンク数に分割します。
// - 合計時間を均等化
// - 要素数に制約なし（最低1個以上）
// - 同じ入力順序とシードなら常に同じ結果
// - chunkCount < 1 の場合は nil を返す
func SplitBalanced(data iter.Seq2[string, time.Duration], chunkCount int, opts ...Option) []Chunk {
	if chunkCount < 1 {
		return nil
	}
	o := options{seed: DefaultSeed}
	for _, opt := range opts {
		opt(&o)
	}
	rng := rand.New(rand.NewPCG(o.seed, o.seed))

	entries := []entry{}
	globalDurMap := make(map[string]time.Duration)
	for k, v := range data {
		globalDurMap[k] = v
		entries = append(entries, entry{Key: k, Dur: v})
	}

	chunks := greedyPartition(entries, chunkCount, rng)
	chunks = simulatedAnnealing(chunks, 50000, 1000.0, 0.01, globalDurMap, rng)

	for i := range chunks {
		var total time.Duration
		for _, k := range chunks[i].Keys {
			total += globalDurMap[k]
		}
		chunks[i].Total = total
	}

	return chunks
}

// --------------------
// 内部関数
// --------------------
func greedyPartition(entries []entry, m int, rng *rand.Rand) []Chunk {
	rng.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })

	chunks := make([]Chunk, m)
	sums := make([]time.Duration, m)
	for _, e := range entries {
		best := 0
		for i := 1; i < m; i++ {
			if sums[i] < sums[best] {
				best = i
			}
		}
		chunks[best].Keys = append(chunks[best].Keys, e.Key)
		sums[best] += e.Dur
		chunks[best].Total = sums[best]
	}
	return chunks
}

func simulatedAnnealing(chunks []Chunk, iterations int, tempStart, tempEnd float64, durMap map[string]time.Duration, rng *rand.Rand) []Chunk {
	best := copyChunks(chunks)
	bestScore := score(best)
	current := copyChunks(chunks)
	currentScore := bestScore

	for i := range iterations {
		t := tempStart * math.Pow(tempEnd/tempStart, float64(i)/float64(iterations))
		next := copyChunks(current)

		if rng.Float64() < 0.5 {
			from := rng.IntN(len(next))
			if len(next[from].Keys) == 0 {
				continue
			}
			to := rng.IntN(len(next))
			if from == to {
				continue
			}
			idx := rng.IntN(len(next[from].Keys))
			val := next[from].Keys[idx]
			next[from].Keys = append(next[from].Keys[:idx], next[from].Keys[idx+1:]...)
			next[to].Keys = append(next[to].Keys, val)
		} else {
			a := rng.IntN(len(next))
			b := rng.IntN(len(next))
			if a == b || len(next[a].Keys) == 0 || len(next[b].Keys) == 0 {
				continue
			}
			ia := rng.IntN(len(next[a].Keys))
			ib := rng.IntN(len(next[b].Keys))
			next[a].Keys[ia], next[b].Keys[ib] = next[b].Keys[ib], next[a].Keys[ia]
		}

		for i := range next {
			var sum time.Duration
			for _, k := range next[i].Keys {
				sum += durMap[k]
			}
			next[i].Total = sum
		}

		nextScore := score(next)
		delta := nextScore - currentScore
		if delta < 0 || rng.Float64() < math.Exp(-delta/t) {
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

// score returns the spread (max - min) of chunk totals in seconds.
func score(chunks []Chunk) float64 {
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
	return max - min
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
