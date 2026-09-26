package focil

import (
	"github.com/Naveen-6087/inclusion-gossip/gossip"
	"github.com/Naveen-6087/inclusion-gossip/sim"
)

// Validator applies the gossip validation rules of the inclusion_list topic
// for one node. Times are measured from the start of slot Slot.
type Validator struct {
	Slot          uint64
	CommitteeRoot [32]byte
	Committee     map[int]bool
	// PerValidator is how many distinct valid lists per committee member are
	// accepted and forwarded: 2 in the specification. Setting it to 1 models
	// a client that drops the second list, so equivocation evidence does not
	// spread.
	PerValidator int
	valid        map[int]int // valid lists seen per validator
}

func NewValidator(slot uint64, root [32]byte, committee []int) *Validator {
	m := make(map[int]bool, len(committee))
	for _, v := range committee {
		m[v] = true
	}
	return &Validator{Slot: slot, CommitteeRoot: root, Committee: m, PerValidator: 2, valid: map[int]int{}}
}

// Validate checks il received at time now. Rules, in specification order:
//
//	[REJECT] transactions within MAX_BYTES_PER_INCLUSION_LIST
//	[IGNORE] slot is the current slot, or the previous slot before the
//	         attestation deadline
//	[IGNORE] committee root matches the committee on this branch
//	[REJECT] validator is in the committee
//	[IGNORE] first or second valid list from this validator
//	[REJECT] signature is valid
//
// The second distinct list is accepted and forwarded so that the evidence of
// an equivocation spreads; later ones are ignored.
func (v *Validator) Validate(il *InclusionList, now sim.Time) gossip.Result {
	if il.TxBytes() > MaxBytesPerInclusionList {
		return gossip.Reject
	}
	switch {
	case il.Slot == v.Slot && now < SecondsPerSlot:
	case il.Slot == v.Slot && now < SecondsPerSlot+AttestationDeadline:
		// Now in the next slot: the list is from the previous slot.
	default:
		return gossip.Ignore
	}
	if il.CommitteeRoot != v.CommitteeRoot {
		return gossip.Ignore
	}
	if !v.Committee[il.Validator] {
		return gossip.Reject
	}
	if v.valid[il.Validator] >= v.PerValidator {
		return gossip.Ignore
	}
	if il.BadSignature {
		return gossip.Reject
	}
	v.valid[il.Validator]++
	return gossip.Accept
}
