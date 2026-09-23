package sim

import (
	"testing"
	"time"
)

func TestOrdering(t *testing.T) {
	s := New(1)
	var got []int
	s.At(30*time.Millisecond, func() { got = append(got, 3) })
	s.At(10*time.Millisecond, func() {
		got = append(got, 1)
		s.After(0, func() { got = append(got, 2) }) // same time, runs after already-queued peers
	})
	s.At(10*time.Millisecond, func() { got = append(got, 11) })
	s.At(50*time.Millisecond, func() { got = append(got, 5) })
	s.Run(40 * time.Millisecond)
	want := []int{1, 11, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if s.Now() != 40*time.Millisecond || s.Pending() != 1 || s.Processed() != 4 {
		t.Fatalf("now %v pending %d processed %d", s.Now(), s.Pending(), s.Processed())
	}
	s.Run(time.Second)
	if got[len(got)-1] != 5 {
		t.Fatal("remaining event not run")
	}
}

func TestPastPanics(t *testing.T) {
	s := New(1)
	s.Run(time.Second)
	defer func() {
		if recover() == nil {
			t.Fatal("scheduling in the past did not panic")
		}
	}()
	s.At(time.Millisecond, func() {})
}

func TestStreamsAreIndependentAndStable(t *testing.T) {
	a1, a2 := Stream(7, "topology"), Stream(7, "topology")
	b := Stream(7, "publish")
	same, diff := true, false
	for range 100 {
		x, y, z := a1.Uint64(), a2.Uint64(), b.Uint64()
		same = same && x == y
		diff = diff || x != z
	}
	if !same || !diff {
		t.Fatalf("same label same stream: %v, labels differ: %v", same, diff)
	}
}
