package sim

import (
	"container/heap"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"time"
)

// Time is simulated time since the start of the slot under study.
type Time = time.Duration

type event struct {
	at  Time
	seq uint64
	fn  func()
}

type queue []event

func (q queue) Len() int { return len(q) }
func (q queue) Less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	return q[i].seq < q[j].seq
}
func (q queue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *queue) Push(x any)   { *q = append(*q, x.(event)) }
func (q *queue) Pop() any {
	old := *q
	e := old[len(old)-1]
	*q = old[:len(old)-1]
	return e
}

// Sim is a single-threaded discrete-event scheduler. Events at the same time
// run in the order they were scheduled, so a run is fully determined by its
// inputs and seed.
type Sim struct {
	now       Time
	q         queue
	seq       uint64
	processed uint64
	seed      uint64
}

func New(seed uint64) *Sim { return &Sim{seed: seed} }

func (s *Sim) Now() Time         { return s.now }
func (s *Sim) Processed() uint64 { return s.processed }
func (s *Sim) Pending() int      { return len(s.q) }

// At schedules fn at absolute time t, which must not be in the past.
func (s *Sim) At(t Time, fn func()) {
	if t < s.now {
		panic(fmt.Sprintf("sim: scheduling at %v, before now (%v)", t, s.now))
	}
	s.seq++
	heap.Push(&s.q, event{at: t, seq: s.seq, fn: fn})
}

// After schedules fn d after the current time.
func (s *Sim) After(d Time, fn func()) { s.At(s.now+d, fn) }

// Run processes events up to and including time until.
func (s *Sim) Run(until Time) {
	for len(s.q) > 0 && s.q[0].at <= until {
		e := heap.Pop(&s.q).(event)
		s.now = e.at
		s.processed++
		e.fn()
	}
	if s.now < until {
		s.now = until
	}
}

// Rand returns a random stream derived from the run seed and a label.
// Separate streams keep, for example, the topology identical when only the
// publish times of an experiment change.
func (s *Sim) Rand(label string) *rand.Rand {
	return Stream(s.seed, label)
}

func Stream(seed uint64, label string) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(label))
	return rand.New(rand.NewPCG(seed, h.Sum64()))
}
