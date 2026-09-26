package focil

import (
	"crypto/sha256"
	"encoding/binary"
	"time"

	"github.com/Naveen-6087/inclusion-gossip/gossip"
)

// Values from EIP-7805 (FOCIL) with 12 second slots.
const (
	CommitteeSize            = 16
	MaxBytesPerInclusionList = 8192
	SecondsPerSlot           = 12 * time.Second
	ViewFreezeDeadline       = 9 * time.Second // SECONDS_PER_SLOT * 3 // 4
	ProposerCutoff           = 11 * time.Second
	AttestationDeadline      = 4 * time.Second // into the following slot
	signatureSize            = 96
)

type Tx struct {
	ID   uint64
	Size int
	Fee  uint64
}

// InclusionList is the message of a SignedInclusionList. Only the fields the
// simulation needs are modelled; the signature is represented by its size
// and a validity flag.
type InclusionList struct {
	Slot          uint64
	Validator     int
	CommitteeRoot [32]byte
	Txs           []Tx
	BadSignature  bool
}

// TxBytes is the size counted against MAX_BYTES_PER_INCLUSION_LIST.
func (il *InclusionList) TxBytes() int {
	n := 0
	for _, t := range il.Txs {
		n += t.Size
	}
	return n
}

// Size is the SSZ-encoded size of the SignedInclusionList: an offset and the
// signature, then slot, validator index, committee root, the transactions
// offset and one offset per transaction.
func (il *InclusionList) Size() int {
	return 4 + signatureSize + 8 + 8 + 32 + 4 + 4*len(il.Txs) + il.TxBytes()
}

// ID identifies a message on the wire. Two lists from the same validator
// with different contents have different IDs, as gossipsub message IDs are
// content hashes.
func (il *InclusionList) ID() gossip.MessageID {
	h := sha256.New()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], il.Slot)
	h.Write(b[:])
	binary.LittleEndian.PutUint64(b[:], uint64(il.Validator))
	h.Write(b[:])
	h.Write(il.CommitteeRoot[:])
	for _, t := range il.Txs {
		binary.LittleEndian.PutUint64(b[:], t.ID)
		h.Write(b[:])
	}
	if il.BadSignature {
		h.Write([]byte{1})
	}
	sum := h.Sum(nil)
	return gossip.MessageID(binary.LittleEndian.Uint64(sum[:8]))
}

func (il *InclusionList) Message() *gossip.Message {
	return &gossip.Message{ID: il.ID(), Size: il.Size(), Payload: il}
}

// Equal compares contents, the way the store detects equivocation.
func (il *InclusionList) Equal(o *InclusionList) bool {
	if il.Slot != o.Slot || il.Validator != o.Validator || il.CommitteeRoot != o.CommitteeRoot || len(il.Txs) != len(o.Txs) {
		return false
	}
	for i := range il.Txs {
		if il.Txs[i] != o.Txs[i] {
			return false
		}
	}
	return true
}
