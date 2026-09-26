package focil

import (
	"cmp"
	"math/rand/v2"
	"slices"
)

type MempoolConfig struct {
	Txs        int     `json:"txs"`          // pending transactions
	MinSize    int     `json:"min_size"`     // bytes
	MaxSize    int     `json:"max_size"`     // bytes
	Visibility float64 `json:"visibility"`   // chance a member's mempool has a given tx
	MaxBytes   int     `json:"max_il_bytes"` // per list, at most MaxBytesPerInclusionList
}

func (c MempoolConfig) WithDefaults() MempoolConfig {
	if c.Txs == 0 {
		c.Txs = 300
	}
	if c.MinSize == 0 {
		c.MinSize = 110
	}
	if c.MaxSize == 0 {
		c.MaxSize = 600
	}
	if c.Visibility == 0 {
		c.Visibility = 0.9
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = MaxBytesPerInclusionList
	}
	return c
}

// Mempool is the set of pending transactions for the slot.
type Mempool struct {
	cfg MempoolConfig
	Txs []Tx
}

func NewMempool(cfg MempoolConfig, rng *rand.Rand) *Mempool {
	cfg = cfg.WithDefaults()
	m := &Mempool{cfg: cfg, Txs: make([]Tx, cfg.Txs)}
	for i := range m.Txs {
		m.Txs[i] = Tx{
			ID:   uint64(i + 1),
			Size: cfg.MinSize + rng.IntN(cfg.MaxSize-cfg.MinSize+1),
			Fee:  rng.Uint64N(1_000_000),
		}
	}
	return m
}

// Build returns the list a committee member would publish: the
// highest-paying transactions it can see, up to the byte limit. Members see
// overlapping but not identical subsets of the mempool.
func (m *Mempool) Build(rng *rand.Rand) []Tx {
	var visible []Tx
	for _, t := range m.Txs {
		if rng.Float64() < m.cfg.Visibility {
			visible = append(visible, t)
		}
	}
	slices.SortStableFunc(visible, func(a, b Tx) int { return cmp.Compare(b.Fee, a.Fee) })
	var out []Tx
	size := 0
	for _, t := range visible {
		if size+t.Size > m.cfg.MaxBytes {
			continue
		}
		out = append(out, t)
		size += t.Size
	}
	slices.SortFunc(out, func(a, b Tx) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Unique returns a transaction that is in nobody's list, which an
// equivocator can put in its second version.
func (m *Mempool) Unique(rng *rand.Rand) Tx {
	return Tx{ID: uint64(len(m.Txs)+1) + rng.Uint64N(1<<32), Size: m.cfg.MinSize, Fee: 0}
}
