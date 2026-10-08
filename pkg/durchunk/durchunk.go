// Package durchunk provides functions to split key-durations into balanced chunks.
package durchunk

import (
	"cmp"
	"iter"
	"math"
	"math/rand/v2"
	"slices"
	"time"
)

// DefaultSeed is the random seed used when WithSeed is not given.
const DefaultSeed uint64 = 1

// DefaultIterations is the number of simulated annealing iterations used when WithIterations is not given.
const DefaultIterations = 50000

// Chunk chunk after balanced split
type Chunk[K comparable] struct {
	Keys  []K           `json:"keys"`
	Total time.Duration `json:"total"`
}

type options struct {
	seed       uint64
	iterations int
}

// Option configures SplitBalanced.
type Option func(*options)

// WithSeed sets the random seed. The same input and seed always produce the same result.
func WithSeed(seed uint64) Option {
	return func(o *options) { o.seed = seed }
}

// WithIterations sets the number of simulated annealing iterations. 0 disables annealing.
func WithIterations(n int) Option {
	return func(o *options) { o.iterations = n }
}

type entry[K comparable] struct {
	key   K
	dur   time.Duration
	order int // position in the input sequence
}

// SplitBalanced splits key-durations into chunkCount chunks so that the largest chunk total
// (the makespan) is minimized, and the chunk totals are as even as possible.
//
// The result is deterministic for the same input order and seed. Keys in each chunk keep the input order.
// It returns nil if chunkCount < 1.
func SplitBalanced[K comparable](data iter.Seq2[K, time.Duration], chunkCount int, opts ...Option) []Chunk[K] {
	if chunkCount < 1 {
		return nil
	}
	o := options{seed: DefaultSeed, iterations: DefaultIterations}
	for _, opt := range opts {
		opt(&o)
	}

	var entries []entry[K]
	for k, d := range data {
		entries = append(entries, entry[K]{key: k, dur: max(d, 0), order: len(entries)})
	}

	assign, sums := greedyPartition(entries, chunkCount)
	if chunkCount > 1 && len(entries) > 1 && o.iterations > 0 {
		rng := rand.New(rand.NewPCG(o.seed, o.seed))
		assign, sums = simulatedAnnealing(entries, assign, sums, o.iterations, rng)
	}

	chunks := make([]Chunk[K], chunkCount)
	for i, e := range entries {
		c := assign[i]
		chunks[c].Keys = append(chunks[c].Keys, e.key)
	}
	for i := range chunks {
		chunks[i].Total = sums[i]
	}
	return chunks
}

// greedyPartition assigns entries using the LPT (Longest Processing Time first) rule.
// entries is sorted in place by input order on return.
func greedyPartition[K comparable](entries []entry[K], m int) (assign []int, sums []time.Duration) {
	slices.SortStableFunc(entries, func(a, b entry[K]) int {
		return cmp.Compare(b.dur, a.dur)
	})
	byOrder := make([]int, len(entries))
	sums = make([]time.Duration, m)
	counts := make([]int, m)
	for _, e := range entries {
		best := 0
		for i := 1; i < m; i++ {
			// prefer fewer keys on ties so that zero-duration keys are spread too
			if sums[i] < sums[best] || (sums[i] == sums[best] && counts[i] < counts[best]) {
				best = i
			}
		}
		byOrder[e.order] = best
		sums[best] += e.dur
		counts[best]++
	}
	slices.SortFunc(entries, func(a, b entry[K]) int { return cmp.Compare(a.order, b.order) })
	return byOrder, sums
}

// simulatedAnnealing improves the assignment by moving or swapping entries between chunks.
// Only the touched chunk sums are updated on each step.
func simulatedAnnealing[K comparable](entries []entry[K], assign []int, sums []time.Duration, iterations int, rng *rand.Rand) ([]int, []time.Duration) {
	m := len(sums)
	n := len(entries)

	var total time.Duration
	for _, s := range sums {
		total += s
	}
	if total == 0 {
		return assign, sums
	}
	// Temperatures are relative to the mean entry duration.
	mean := float64(total) / float64(n)
	tempStart, tempEnd := mean, mean*1e-4

	cur := slices.Clone(assign)
	curSums := slices.Clone(sums)
	curScore := score(curSums)
	best := slices.Clone(cur)
	bestSums := slices.Clone(curSums)
	bestScore := curScore

	for i := range iterations {
		t := tempStart * math.Pow(tempEnd/tempStart, float64(i)/float64(iterations))

		a := rng.IntN(n)
		ca := cur[a]
		var b, cb int
		swap := rng.IntN(2) == 0
		if swap {
			b = rng.IntN(n)
			cb = cur[b]
		} else {
			cb = rng.IntN(m)
		}
		if ca == cb {
			continue
		}

		// apply
		curSums[ca] -= entries[a].dur
		curSums[cb] += entries[a].dur
		cur[a] = cb
		if swap {
			curSums[cb] -= entries[b].dur
			curSums[ca] += entries[b].dur
			cur[b] = ca
		}

		nextScore := score(curSums)
		delta := nextScore - curScore
		if delta <= 0 || rng.Float64() < math.Exp(-delta/t) {
			curScore = nextScore
			if curScore < bestScore {
				bestScore = curScore
				copy(best, cur)
				copy(bestSums, curSums)
			}
			continue
		}

		// revert
		if swap {
			curSums[ca] -= entries[b].dur
			curSums[cb] += entries[b].dur
			cur[b] = cb
		}
		curSums[cb] -= entries[a].dur
		curSums[ca] += entries[a].dur
		cur[a] = ca
	}
	return best, bestSums
}

// score is primarily the makespan (max chunk total), with the spread (max - min) as a tie-breaker.
func score(sums []time.Duration) float64 {
	lo, hi := slices.Min(sums), slices.Max(sums)
	return float64(hi) + float64(hi-lo)/float64(len(sums))
}
