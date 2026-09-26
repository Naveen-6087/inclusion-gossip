package focil

import (
	"slices"

	"github.com/Naveen-6087/inclusion-gossip/sim"
)

type stored struct {
	il *InclusionList
	at sim.Time
}

// Store is one node's inclusion-list state for a single slot, following
// on_inclusion_list in the EIP-7805 fork-choice specification:
//
//   - a list from a known equivocator is dropped;
//   - a list that differs from the one already stored for its validator
//     makes that validator an equivocator;
//   - otherwise the list is stored, but only before the store's freeze time.
//
// The times at which lists were stored and equivocations detected are kept so
// a view can be evaluated at any later moment.
type Store struct {
	freeze       sim.Time
	lists        map[int]stored
	equivocators map[int]sim.Time
}

// NewStore returns a store that accepts lists until freeze: the view-freeze
// deadline for attesters, or the proposer's cutoff.
func NewStore(freeze sim.Time) *Store {
	return &Store{freeze: freeze, lists: map[int]stored{}, equivocators: map[int]sim.Time{}}
}

func (s *Store) OnInclusionList(il *InclusionList, now sim.Time) {
	v := il.Validator
	if _, eq := s.equivocators[v]; eq {
		return
	}
	if prev, ok := s.lists[v]; ok {
		if !prev.il.Equal(il) {
			s.equivocators[v] = now
		}
		return
	}
	if now < s.freeze {
		s.lists[v] = stored{il, now}
	}
}

// Equivocator reports whether v was detected as an equivocator by time at.
func (s *Store) Equivocator(v int, at sim.Time) bool {
	t, ok := s.equivocators[v]
	return ok && t <= at
}

// View returns the validators whose lists count at time at: stored, and not
// detected as equivocating by then. The result is sorted.
func (s *Store) View(at sim.Time) []int {
	var out []int
	for v, st := range s.lists {
		if st.at <= at && !s.Equivocator(v, at) {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

// Transactions returns the union of transaction IDs in the view at time at,
// sorted (get_inclusion_list_transactions).
func (s *Store) Transactions(at sim.Time) []uint64 {
	seen := map[uint64]bool{}
	var out []uint64
	for _, v := range s.View(at) {
		for _, t := range s.lists[v].il.Txs {
			if !seen[t.ID] {
				seen[t.ID] = true
				out = append(out, t.ID)
			}
		}
	}
	slices.Sort(out)
	return out
}

// StoredAt returns when v's list was stored.
func (s *Store) StoredAt(v int) (sim.Time, bool) {
	st, ok := s.lists[v]
	return st.at, ok
}

// Subset reports whether every element of a (sorted) is in b (sorted).
func Subset(a, b []uint64) bool {
	j := 0
	for _, x := range a {
		for j < len(b) && b[j] < x {
			j++
		}
		if j == len(b) || b[j] != x {
			return false
		}
	}
	return true
}
