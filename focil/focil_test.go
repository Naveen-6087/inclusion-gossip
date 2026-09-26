package focil

import (
	"slices"
	"testing"
	"time"

	"github.com/Naveen-6087/inclusion-gossip/gossip"
	"github.com/Naveen-6087/inclusion-gossip/sim"
)

var root = [32]byte{1}

func il(v int, txs ...uint64) *InclusionList {
	l := &InclusionList{Slot: 10, Validator: v, CommitteeRoot: root}
	for _, id := range txs {
		l.Txs = append(l.Txs, Tx{ID: id, Size: 100})
	}
	return l
}

func ms(n int) sim.Time { return time.Duration(n) * time.Millisecond }

func TestStoreFollowsSpec(t *testing.T) {
	s := NewStore(ViewFreezeDeadline)
	s.OnInclusionList(il(1, 10, 11), ms(8000))
	s.OnInclusionList(il(2, 20), ms(8100))
	s.OnInclusionList(il(1, 10, 11), ms(8200)) // same list again: not an equivocation
	if got := s.View(ms(8500)); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("view %v", got)
	}
	// A list arriving after the freeze deadline is not stored...
	s.OnInclusionList(il(3, 30), ms(9100))
	// ...but a conflicting list from a stored validator is still evidence.
	s.OnInclusionList(il(2, 21), ms(9500))
	if got := s.View(ms(9400)); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("view before detection %v", got)
	}
	if got := s.View(ms(9500)); !slices.Equal(got, []int{1}) {
		t.Fatalf("view after detection %v", got)
	}
	if !s.Equivocator(2, ms(9500)) || s.Equivocator(2, ms(9499)) {
		t.Fatal("detection time")
	}
	// Further lists from an equivocator are dropped, even identical ones.
	s.OnInclusionList(il(2, 20), ms(9600))
	if got := s.Transactions(ms(10000)); !slices.Equal(got, []uint64{10, 11}) {
		t.Fatalf("transactions %v", got)
	}
	// A second list from a validator whose first arrived late (unstored)
	// is not an equivocation either.
	s.OnInclusionList(il(3, 31), ms(9700))
	if s.Equivocator(3, ms(20000)) {
		t.Fatal("unstored validator marked as equivocator")
	}
}

func TestProposerStoreHasLaterCutoff(t *testing.T) {
	att, prop := NewStore(ViewFreezeDeadline), NewStore(ProposerCutoff)
	for _, s := range []*Store{att, prop} {
		s.OnInclusionList(il(5, 50), ms(10000))
	}
	if len(att.View(ms(13000))) != 0 || len(prop.View(ms(11000))) != 1 {
		t.Fatal("cutoffs not applied")
	}
}

func TestTransactionsUnion(t *testing.T) {
	s := NewStore(ViewFreezeDeadline)
	s.OnInclusionList(il(1, 5, 7, 9), 0)
	s.OnInclusionList(il(2, 7, 8), 0)
	if got := s.Transactions(ms(1)); !slices.Equal(got, []uint64{5, 7, 8, 9}) {
		t.Fatalf("union %v", got)
	}
}

func TestSubset(t *testing.T) {
	cases := []struct {
		a, b []uint64
		want bool
	}{
		{nil, nil, true},
		{[]uint64{1, 3}, []uint64{1, 2, 3}, true},
		{[]uint64{1, 4}, []uint64{1, 2, 3}, false},
		{[]uint64{5}, nil, false},
	}
	for _, c := range cases {
		if Subset(c.a, c.b) != c.want {
			t.Errorf("Subset(%v, %v) != %v", c.a, c.b, c.want)
		}
	}
}

func TestGossipValidation(t *testing.T) {
	v := NewValidator(10, root, []int{1, 2, 3})
	check := func(l *InclusionList, now sim.Time, want gossip.Result) {
		t.Helper()
		if got := v.Validate(l, now); got != want {
			t.Errorf("validator %d at %v: got %v, want %v", l.Validator, now, got, want)
		}
	}
	big := il(1)
	for i := range 9 {
		big.Txs = append(big.Txs, Tx{ID: uint64(i), Size: 1000})
	}
	check(big, ms(1), gossip.Reject)
	wrongSlot := il(1, 1)
	wrongSlot.Slot = 9
	check(wrongSlot, ms(1), gossip.Ignore)
	wrongRoot := il(1, 1)
	wrongRoot.CommitteeRoot = [32]byte{2}
	check(wrongRoot, ms(1), gossip.Ignore)
	check(il(7, 1), ms(1), gossip.Reject) // not in the committee
	badSig := il(2, 1)
	badSig.BadSignature = true
	check(badSig, ms(1), gossip.Reject)

	check(il(1, 1), ms(8000), gossip.Accept)
	check(il(1, 2), ms(8500), gossip.Accept) // second version: forwarded as evidence
	check(il(1, 3), ms(8600), gossip.Ignore) // third: ignored
	// Previous-slot lists are accepted until the attestation deadline.
	check(il(3, 1), SecondsPerSlot+ms(3999), gossip.Accept)
	check(il(2, 1), SecondsPerSlot+ms(4000), gossip.Ignore)
}

func TestFirstListOnly(t *testing.T) {
	v := NewValidator(10, root, []int{1})
	v.PerValidator = 1
	if v.Validate(il(1, 1), ms(1)) != gossip.Accept || v.Validate(il(1, 2), ms(2)) != gossip.Ignore {
		t.Fatal("second list not ignored")
	}
}

func TestSizeAndID(t *testing.T) {
	a := il(1, 1, 2)
	if a.TxBytes() != 200 || a.Size() != 4+96+8+8+32+4+8+200 {
		t.Fatalf("size %d", a.Size())
	}
	b := il(1, 1, 2)
	if a.ID() != b.ID() || !a.Equal(b) {
		t.Fatal("equal lists differ")
	}
	c := il(1, 1, 3)
	if a.ID() == c.ID() || a.Equal(c) {
		t.Fatal("different lists collide")
	}
	if m := a.Message(); m.Size != a.Size() || m.Payload != a {
		t.Fatal("message")
	}
}

func TestMempoolBuild(t *testing.T) {
	m := NewMempool(MempoolConfig{}, sim.Stream(1, "mempool"))
	var lists [][]Tx
	for i := range CommitteeSize {
		l := m.Build(sim.Stream(uint64(i), "member"))
		size := 0
		for j, tx := range l {
			size += tx.Size
			if j > 0 && l[j-1].ID >= tx.ID {
				t.Fatal("list not sorted by ID")
			}
		}
		if size > MaxBytesPerInclusionList || size < MaxBytesPerInclusionList-600 {
			t.Fatalf("list of %d bytes", size)
		}
		lists = append(lists, l)
	}
	// Members mostly agree on the best-paying transactions.
	inAll := 0
	for _, tx := range lists[0] {
		all := true
		for _, l := range lists[1:] {
			if !slices.ContainsFunc(l, func(x Tx) bool { return x.ID == tx.ID }) {
				all = false
			}
		}
		if all {
			inAll++
		}
	}
	if inAll == 0 || inAll == len(lists[0]) {
		t.Fatalf("%d of %d transactions in every list: expected partial overlap", inAll, len(lists[0]))
	}
	u := m.Unique(sim.Stream(1, "u"))
	for _, l := range lists {
		if slices.ContainsFunc(l, func(x Tx) bool { return x.ID == u.ID }) {
			t.Fatal("unique transaction already listed")
		}
	}
}
