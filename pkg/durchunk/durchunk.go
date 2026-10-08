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
	Keys []K `json:"keys"`
	// Total is the sum of durations of Keys plus the costs of the groups in the chunk.
	Total time.Duration `json:"total"`
}

// Item is a key with its duration, optionally belonging to a group.
type Item[K comparable] struct {
	Key      K
	Duration time.Duration
	// Group is the group of the item. Each chunk containing at least one item of a group
	// pays the cost of the group once. Empty means no group.
	Group string
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

// SplitBalanced splits key-durations into chunkCount chunks so that the largest chunk total
// (the makespan) is minimized, and the chunk totals are as even as possible.
//
// The result is deterministic for the same input order and seed. Keys in each chunk keep the input order.
// It returns nil if chunkCount < 1.
func SplitBalanced[K comparable](data iter.Seq2[K, time.Duration], chunkCount int, opts ...Option) []Chunk[K] {
	var items []Item[K]
	for k, d := range data {
		items = append(items, Item[K]{Key: k, Duration: d})
	}
	return SplitBalancedItems(items, chunkCount, nil, opts...)
}

// SplitBalancedItems is like SplitBalanced, but items can belong to groups with a fixed cost
// (e.g. the startup cost of a process), which is added once to each chunk containing the group.
// This favors keeping items of a costly group in the same chunk.
// groupCost may be nil, which means all groups cost nothing.
func SplitBalancedItems[K comparable](items []Item[K], chunkCount int, groupCost func(group string) time.Duration, opts ...Option) []Chunk[K] {
	if chunkCount < 1 {
		return nil
	}
	o := options{seed: DefaultSeed, iterations: DefaultIterations}
	for _, opt := range opts {
		opt(&o)
	}

	s := newState(items, chunkCount, groupCost)
	s.greedy()
	if chunkCount > 1 && len(items) > 1 && o.iterations > 0 {
		s.anneal(o.iterations, rand.New(rand.NewPCG(o.seed, o.seed)))
	}

	chunks := make([]Chunk[K], chunkCount)
	for i, it := range items {
		c := s.best[i]
		chunks[c].Keys = append(chunks[c].Keys, it.Key)
	}
	for i := range chunks {
		chunks[i].Total = s.bestSums[i]
	}
	return chunks
}

type entry struct {
	dur   time.Duration
	group int // index into state.costs, -1 for no group
}

// state holds an assignment of entries to chunks. Chunk sums include group costs.
type state struct {
	entries []entry
	costs   []time.Duration
	m       int

	assign []int
	sums   []time.Duration
	counts [][]int // [chunk][group] number of entries

	best     []int
	bestSums []time.Duration
}

func newState[K comparable](items []Item[K], m int, groupCost func(string) time.Duration) *state {
	s := &state{m: m}
	groups := make(map[string]int)
	for _, it := range items {
		g := -1
		if it.Group != "" {
			var ok bool
			if g, ok = groups[it.Group]; !ok {
				g = len(s.costs)
				groups[it.Group] = g
				var cost time.Duration
				if groupCost != nil {
					cost = max(groupCost(it.Group), 0)
				}
				s.costs = append(s.costs, cost)
			}
		}
		s.entries = append(s.entries, entry{dur: max(it.Duration, 0), group: g})
	}
	s.assign = make([]int, len(s.entries))
	s.sums = make([]time.Duration, m)
	s.counts = make([][]int, m)
	for i := range s.counts {
		s.counts[i] = make([]int, len(s.costs))
	}
	return s
}

// addCost returns how much the sum of chunk c increases by adding entry i.
func (s *state) addCost(i, c int) time.Duration {
	e := s.entries[i]
	if e.group >= 0 && s.counts[c][e.group] == 0 {
		return e.dur + s.costs[e.group]
	}
	return e.dur
}

func (s *state) add(i, c int) {
	s.sums[c] += s.addCost(i, c)
	if g := s.entries[i].group; g >= 0 {
		s.counts[c][g]++
	}
	s.assign[i] = c
}

func (s *state) remove(i, c int) {
	e := s.entries[i]
	s.sums[c] -= e.dur
	if e.group >= 0 {
		s.counts[c][e.group]--
		if s.counts[c][e.group] == 0 {
			s.sums[c] -= s.costs[e.group]
		}
	}
}

func (s *state) move(i, to int) {
	s.remove(i, s.assign[i])
	s.add(i, to)
}

// greedy assigns entries using the LPT (Longest Processing Time first) rule.
func (s *state) greedy() {
	order := make([]int, len(s.entries))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(s.entries[b].dur, s.entries[a].dur)
	})
	sizes := make([]int, s.m)
	for _, i := range order {
		best := 0
		for c := 1; c < s.m; c++ {
			nc, nb := s.sums[c]+s.addCost(i, c), s.sums[best]+s.addCost(i, best)
			// prefer fewer entries on ties so that zero-duration entries are spread too
			if nc < nb || (nc == nb && sizes[c] < sizes[best]) {
				best = c
			}
		}
		s.add(i, best)
		sizes[best]++
	}
	s.best = slices.Clone(s.assign)
	s.bestSums = slices.Clone(s.sums)
}

// anneal improves the assignment by moving or swapping entries between chunks.
func (s *state) anneal(iterations int, rng *rand.Rand) {
	n := len(s.entries)
	var total time.Duration
	for _, sum := range s.sums {
		total += sum
	}
	if total == 0 {
		return
	}
	// Temperatures are relative to the mean entry duration.
	mean := float64(total) / float64(n)
	tempStart, tempEnd := mean, mean*1e-4

	curScore := score(s.sums)
	bestScore := curScore
	for it := range iterations {
		t := tempStart * math.Pow(tempEnd/tempStart, float64(it)/float64(iterations))

		a := rng.IntN(n)
		ca := s.assign[a]
		b, cb := -1, rng.IntN(s.m)
		if rng.IntN(2) == 0 { // swap
			b = rng.IntN(n)
			cb = s.assign[b]
		}
		if ca == cb {
			continue
		}

		s.move(a, cb)
		if b >= 0 {
			s.move(b, ca)
		}

		nextScore := score(s.sums)
		delta := nextScore - curScore
		if delta <= 0 || rng.Float64() < math.Exp(-delta/t) {
			curScore = nextScore
			if curScore < bestScore {
				bestScore = curScore
				copy(s.best, s.assign)
				copy(s.bestSums, s.sums)
			}
			continue
		}

		// revert
		if b >= 0 {
			s.move(b, cb)
		}
		s.move(a, ca)
	}
}

// score is primarily the makespan (max chunk total), with the spread (max - min) as a tie-breaker.
func score(sums []time.Duration) float64 {
	lo, hi := slices.Min(sums), slices.Max(sums)
	return float64(hi) + float64(hi-lo)/float64(len(sums))
}
