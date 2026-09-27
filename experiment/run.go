package experiment

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/Naveen-6087/inclusion-gossip/focil"
	"github.com/Naveen-6087/inclusion-gossip/gossip"
	"github.com/Naveen-6087/inclusion-gossip/network"
	"github.com/Naveen-6087/inclusion-gossip/sim"
)

const slot = 100

// ListResult describes how one committee member's list spread.
type ListResult struct {
	Validator   int     `json:"validator"`
	Kind        string  `json:"kind"`
	PublishedMs float64 `json:"published_ms"`
	// Shares of the other nodes that had the list (any version) by the
	// view-freeze deadline, the proposer cutoff and the end of the run.
	AtFreeze float64 `json:"at_freeze"`
	AtCutoff float64 `json:"at_cutoff"`
	Final    float64 `json:"final"`
	// Delivery latency after publication over the nodes that received it.
	P50Ms        float64 `json:"p50_ms"`
	P90Ms        float64 `json:"p90_ms"`
	P99Ms        float64 `json:"p99_ms"`
	MaxMs        float64 `json:"max_ms"`
	InProposer   bool    `json:"in_proposer_view"`
	AttesterView float64 `json:"attester_view"` // share of attesters that count it
}

type EquivocationResult struct {
	Validator int `json:"validator"`
	// Shares of attesters that had detected the equivocation by the freeze
	// deadline and by the attester check.
	DetectedAtFreeze float64 `json:"detected_at_freeze"`
	DetectedAtCheck  float64 `json:"detected_at_check"`
	ProposerDetected bool    `json:"proposer_detected"`
}

// RunResult is the outcome of simulating one slot with one seed.
type RunResult struct {
	Seed      uint64 `json:"seed"`
	Events    uint64 `json:"events"`
	Proposer  int    `json:"proposer"`
	Attesters int    `json:"attesters"`
	// Share of attesters whose counted lists are not all in the proposer's
	// view, and share whose required transactions are not all in the
	// proposer's: the latter would judge the next block as not satisfying
	// the inclusion lists.
	ListMismatch  float64 `json:"list_mismatch"`
	TxUnsatisfied float64 `json:"tx_unsatisfied"`
	ProposerLists int     `json:"proposer_lists"`
	MeanLists     float64 `json:"mean_attester_lists"`

	Lists         []ListResult         `json:"lists"`
	Equivocations []EquivocationResult `json:"equivocations,omitempty"`

	MeanBytesSent float64      `json:"mean_bytes_sent"`
	MaxBytesSent  int64        `json:"max_bytes_sent"`
	MeanDups      float64      `json:"mean_duplicates"`
	Dropped       int64        `json:"dropped"`
	Gossip        gossip.Stats `json:"gossip"`
}

type member struct {
	node int
	kind string
	adv  *Adversary
}

func committeeRoot(committee []int) [32]byte {
	h := sha256.New()
	var b [8]byte
	for _, v := range committee {
		binary.LittleEndian.PutUint64(b[:], uint64(v))
		h.Write(b[:])
	}
	var r [32]byte
	h.Sum(r[:0])
	return r
}

// RunSlot simulates the inclusion lists of one slot. cfg must have defaults
// applied and be valid.
func RunSlot(cfg Config, seed uint64) (RunResult, error) {
	s := sim.New(seed)
	net, err := network.Build(cfg.Network, s.Rand("topology"))
	if err != nil {
		return RunResult{}, err
	}
	tr := network.NewTransport(net, s)
	n := net.Size()
	tl := cfg.Timeline
	end := tl.AttesterCheck.Duration

	roles := s.Rand("roles")
	perm := roles.Perm(n)
	committee := slices.Clone(perm[:focil.CommitteeSize])
	proposer := perm[focil.CommitteeSize]
	root := committeeRoot(committee)

	members := make([]member, len(committee))
	adversarial := map[int]bool{}
	i := 0
	for ai := range cfg.Adversaries {
		a := &cfg.Adversaries[ai]
		for range a.N() {
			members[i] = member{committee[i], a.Kind, a}
			adversarial[committee[i]] = true
			i++
		}
	}
	for ; i < len(members); i++ {
		members[i] = member{committee[i], "honest", nil}
	}

	validators := make([]*focil.Validator, n)
	stores := make([]*focil.Store, n)
	for v := range n {
		validators[v] = focil.NewValidator(slot, root, committee)
		validators[v].PerValidator = cfg.Rules.ListsPerValidator
		stores[v] = focil.NewStore(tl.Freeze.Duration)
	}
	if tl.ProposerView == "cutoff" {
		stores[proposer] = focil.NewStore(tl.ProposerCutoff.Duration)
	}
	// firstSeen[m][v] is when node v first received any list of member m.
	index := map[int]int{}
	firstSeen := make([][]sim.Time, len(members))
	for mi, m := range members {
		index[m.node] = mi
		firstSeen[mi] = make([]sim.Time, n)
		for v := range firstSeen[mi] {
			firstSeen[mi][v] = -1
		}
	}

	router := gossip.New(s, net, tr, cfg.Gossip.Params(), gossip.Hooks{
		Validate: func(v int, m *gossip.Message) gossip.Result {
			return validators[v].Validate(m.Payload.(*focil.InclusionList), s.Now())
		},
		Deliver: func(v int, m *gossip.Message) {
			il := m.Payload.(*focil.InclusionList)
			stores[v].OnInclusionList(il, s.Now())
			if fs := firstSeen[index[il.Validator]]; fs[v] < 0 {
				fs[v] = s.Now()
			}
		},
	})
	router.Start(end)

	mempool := focil.NewMempool(cfg.Mempool, s.Rand("mempool"))
	publishAt := make([]sim.Time, len(members))
	publish := s.Rand("publish")
	for mi, m := range members {
		il := &focil.InclusionList{
			Slot: slot, Validator: m.node, CommitteeRoot: root,
			Txs: mempool.Build(s.Rand(fmt.Sprintf("member-%d", mi))),
		}
		switch m.kind {
		case "honest":
			at := cfg.Honest.At.Duration - time.Duration(publish.Float64()*float64(cfg.Honest.Jitter.Duration))
			publishAt[mi] = at
			s.At(at, func() { router.Publish(m.node, il.Message(), nil) })
		case KindLate:
			publishAt[mi] = m.adv.At.Duration
			s.At(m.adv.At.Duration, func() { router.Publish(m.node, il.Message(), nil) })
		case KindInvalid:
			il.BadSignature = true
			publishAt[mi] = m.adv.At.Duration
			s.At(m.adv.At.Duration, func() { router.Publish(m.node, il.Message(), nil) })
		case KindSilent:
			publishAt[mi] = -1
		case KindEquivocate:
			b := *il
			b.Txs = slices.Clone(il.Txs)
			b.Txs[len(b.Txs)-1] = mempool.Unique(publish)
			peers := net.Peers(m.node)
			publish.Shuffle(len(peers), func(i, j int) { peers[i], peers[j] = peers[j], peers[i] })
			k := int(math.Round(m.adv.Split * float64(len(peers))))
			toA, toB := peers[:k], peers[k:]
			publishAt[mi] = m.adv.At.Duration
			s.At(m.adv.At.Duration, func() { router.Publish(m.node, il.Message(), toA) })
			s.At(m.adv.SecondAt.Duration, func() { router.Publish(m.node, b.Message(), toB) })
		}
	}

	s.Run(end)
	return measure(cfg, seed, s, net, tr, router, members, publishAt, firstSeen, stores, proposer, adversarial), nil
}

func measure(cfg Config, seed uint64, s *sim.Sim, net *network.Network, tr *network.Transport, router *gossip.Router,
	members []member, publishAt []sim.Time, firstSeen [][]sim.Time, stores []*focil.Store, proposer int, adversarial map[int]bool) RunResult {
	tl := cfg.Timeline
	n := net.Size()
	r := RunResult{Seed: seed, Events: s.Processed(), Proposer: proposer, Dropped: tr.Dropped, Gossip: router.Stats}

	pView := stores[proposer].View(tl.ProposerCutoff.Duration)
	pTxs := stores[proposer].Transactions(tl.ProposerCutoff.Duration)
	r.ProposerLists = len(pView)

	var attesters []int
	for v := range n {
		if v != proposer && !adversarial[v] {
			attesters = append(attesters, v)
		}
	}
	r.Attesters = len(attesters)
	counted := map[int]int{}
	mismatch, unsatisfied, lists := 0, 0, 0
	for _, a := range attesters {
		view := stores[a].View(tl.AttesterCheck.Duration)
		lists += len(view)
		for _, v := range view {
			counted[v]++
		}
		if !subsetInts(view, pView) {
			mismatch++
		}
		if !focil.Subset(stores[a].Transactions(tl.AttesterCheck.Duration), pTxs) {
			unsatisfied++
		}
	}
	na := float64(len(attesters))
	r.ListMismatch = float64(mismatch) / na
	r.TxUnsatisfied = float64(unsatisfied) / na
	r.MeanLists = float64(lists) / na

	for mi, m := range members {
		lr := ListResult{Validator: m.node, Kind: m.kind, PublishedMs: ms(publishAt[mi])}
		var lat []float64
		freeze, cutoff, final := 0, 0, 0
		for v, t := range firstSeen[mi] {
			if v == m.node || t < 0 {
				continue
			}
			final++
			if t < tl.Freeze.Duration {
				freeze++
			}
			if t < tl.ProposerCutoff.Duration {
				cutoff++
			}
			lat = append(lat, ms(t-publishAt[mi]))
		}
		others := float64(n - 1)
		lr.AtFreeze, lr.AtCutoff, lr.Final = float64(freeze)/others, float64(cutoff)/others, float64(final)/others
		slices.Sort(lat)
		lr.P50Ms, lr.P90Ms, lr.P99Ms = quantile(lat, 0.5), quantile(lat, 0.9), quantile(lat, 0.99)
		if len(lat) > 0 {
			lr.MaxMs = lat[len(lat)-1]
		}
		lr.InProposer = slices.Contains(pView, m.node)
		lr.AttesterView = float64(counted[m.node]) / na
		r.Lists = append(r.Lists, lr)

		if m.kind == KindEquivocate {
			er := EquivocationResult{Validator: m.node}
			for _, a := range attesters {
				if stores[a].Equivocator(m.node, tl.Freeze.Duration) {
					er.DetectedAtFreeze++
				}
				if stores[a].Equivocator(m.node, tl.AttesterCheck.Duration) {
					er.DetectedAtCheck++
				}
			}
			er.DetectedAtFreeze /= na
			er.DetectedAtCheck /= na
			er.ProposerDetected = stores[proposer].Equivocator(m.node, tl.ProposerCutoff.Duration)
			r.Equivocations = append(r.Equivocations, er)
		}
	}

	var bytes, dups int64
	for v := range n {
		bytes += tr.BytesSent[v]
		r.MaxBytesSent = max(r.MaxBytesSent, tr.BytesSent[v])
		dups += router.Duplicates(v)
	}
	r.MeanBytesSent = float64(bytes) / float64(n)
	r.MeanDups = float64(dups) / float64(n)
	return r
}

func ms(t sim.Time) float64 {
	if t < 0 {
		return -1
	}
	return float64(t) / float64(time.Millisecond)
}

// quantile of sorted xs, nearest rank; NaN-free: 0 for an empty slice.
func quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	i := int(math.Ceil(q*float64(len(xs)))) - 1
	return xs[max(0, min(i, len(xs)-1))]
}

func subsetInts(a, b []int) bool {
	for _, x := range a {
		if _, ok := slices.BinarySearch(b, x); !ok {
			return false
		}
	}
	return true
}
