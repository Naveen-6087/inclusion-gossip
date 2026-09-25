package gossip

import (
	"math/rand/v2"
	"slices"
	"time"

	"github.com/Naveen-6087/inclusion-gossip/network"
	"github.com/Naveen-6087/inclusion-gossip/sim"
)

type MessageID uint64

type Message struct {
	ID      MessageID
	Size    int // payload bytes on the wire
	Payload any
}

// Result is the outcome of topic validation, as in libp2p.
type Result int

const (
	Accept Result = iota // deliver and forward
	Ignore               // drop without penalty
	Reject               // drop; invalid
)

// Params are the GossipSub v1.1/v1.2 parameters that matter at the time
// scale of one slot. Mesh maintenance (GRAFT/PRUNE) is not simulated: the
// mesh is built once and stays fixed for the run.
type Params struct {
	D, DLow, DHigh, DLazy int
	Heartbeat             time.Duration
	HistoryLength         int     // heartbeats a message stays in the cache
	HistoryGossip         int     // heartbeats a message is advertised in IHAVE
	GossipFactor          float64 // share of non-mesh peers that receive IHAVE
	FloodPublish          bool    // publish to every connected peer, not just the mesh
	IDontWant             bool    // GossipSub v1.2 IDONTWANT
	IDontWantMinSize      int     // only for messages at least this large
	IWantFollowup         time.Duration
	ValidationDelay       time.Duration
	FrameOverhead         int // bytes added to every RPC
	IDSize                int // bytes per message ID in control messages
}

// DefaultParams follows the Ethereum consensus p2p specification (D=8,
// 0.7 s heartbeat, mcache 6/3) and rust-libp2p defaults for the rest.
func DefaultParams() Params {
	return Params{
		D: 8, DLow: 6, DHigh: 12, DLazy: 6,
		Heartbeat:        700 * time.Millisecond,
		HistoryLength:    6,
		HistoryGossip:    3,
		GossipFactor:     0.25,
		FloodPublish:     true,
		IDontWant:        true,
		IDontWantMinSize: 1000,
		IWantFollowup:    3 * time.Second,
		ValidationDelay:  2 * time.Millisecond,
		FrameOverhead:    40,
		IDSize:           20,
	}
}

// Hooks connect the router to the application.
type Hooks struct {
	Validate func(node int, m *Message) Result
	Deliver  func(node int, m *Message) // after Accept, and for the publisher
}

type Stats struct {
	Received      int64 // full messages arriving at a node
	Duplicates    int64 // of which already seen
	Accepted      int64
	Ignored       int64
	Rejected      int64
	Forwarded     int64
	IHave         int64
	IWant         int64
	IDontWant     int64
	SkippedByIDW  int64 // sends avoided because the peer sent IDONTWANT
	ServedByIWant int64
}

type peerState struct {
	peers     []int
	mesh      []int
	seen      map[MessageID]bool
	cache     map[MessageID]*Message
	windows   [][]MessageID // windows[0] is the current heartbeat
	dontWant  map[int]map[MessageID]bool
	requested map[MessageID]bool
	dups      int64
}

type Router struct {
	sim   *sim.Sim
	tr    *network.Transport
	p     Params
	hooks Hooks
	rng   *rand.Rand
	nodes []*peerState
	Stats Stats
}

// New builds the mesh over the connection graph of net.
func New(s *sim.Sim, net *network.Network, tr *network.Transport, p Params, hooks Hooks) *Router {
	r := &Router{sim: s, tr: tr, p: p, hooks: hooks, rng: s.Rand("gossip")}
	r.nodes = make([]*peerState, net.Size())
	for v := range r.nodes {
		r.nodes[v] = &peerState{
			peers:     net.Peers(v),
			seen:      map[MessageID]bool{},
			cache:     map[MessageID]*Message{},
			windows:   [][]MessageID{nil},
			dontWant:  map[int]map[MessageID]bool{},
			requested: map[MessageID]bool{},
		}
	}
	r.buildMesh(s.Rand("mesh"))
	return r
}

// buildMesh grafts every node to D of its peers where both sides have room
// below DHigh, as the mesh would look after a few heartbeats of GRAFTs.
func (r *Router) buildMesh(rng *rand.Rand) {
	order := rng.Perm(len(r.nodes))
	for _, v := range order {
		cand := slices.Clone(r.nodes[v].peers)
		rng.Shuffle(len(cand), func(i, j int) { cand[i], cand[j] = cand[j], cand[i] })
		for _, c := range cand {
			if len(r.nodes[v].mesh) >= r.p.D {
				break
			}
			if len(r.nodes[c].mesh) >= r.p.DHigh || slices.Contains(r.nodes[v].mesh, c) {
				continue
			}
			r.nodes[v].mesh = append(r.nodes[v].mesh, c)
			r.nodes[c].mesh = append(r.nodes[c].mesh, v)
		}
	}
	for _, n := range r.nodes {
		slices.Sort(n.mesh)
	}
}

func (r *Router) Mesh(v int) []int { return r.nodes[v].mesh }

// Duplicates returns how many already-seen messages node v received.
func (r *Router) Duplicates(v int) int64 { return r.nodes[v].dups }

// Start schedules heartbeats for every node, each with a random phase,
// until the given time.
func (r *Router) Start(until sim.Time) {
	for v := range r.nodes {
		phase := time.Duration(r.rng.Int64N(int64(r.p.Heartbeat)))
		var beat func()
		beat = func() {
			r.heartbeat(v)
			if next := r.sim.Now() + r.p.Heartbeat; next <= until {
				r.sim.At(next, beat)
			}
		}
		r.sim.At(phase, beat)
	}
}

// Publish sends m from node v. With targets nil the message goes to every
// connected peer (flood publishing) or to the mesh; a non-nil list sends it
// only to those peers, which is how a split-view equivocator behaves.
func (r *Router) Publish(v int, m *Message, targets []int) {
	n := r.nodes[v]
	if n.seen[m.ID] {
		return
	}
	n.seen[m.ID] = true
	r.remember(v, m)
	if r.hooks.Deliver != nil {
		r.hooks.Deliver(v, m)
	}
	if targets == nil {
		targets = n.mesh
		if r.p.FloodPublish {
			targets = n.peers
		}
	}
	for _, p := range targets {
		r.sendMessage(v, p, m)
	}
}

func (r *Router) remember(v int, m *Message) {
	n := r.nodes[v]
	n.cache[m.ID] = m
	n.windows[0] = append(n.windows[0], m.ID)
}

func (r *Router) sendMessage(from, to int, m *Message) {
	r.tr.Send(from, to, m.Size+r.p.FrameOverhead, func() { r.receive(to, from, m) })
}

func (r *Router) sendControl(from, to, ids int, handle func()) {
	r.tr.Send(from, to, r.p.FrameOverhead+ids*r.p.IDSize, handle)
}

func (r *Router) receive(v, from int, m *Message) {
	n := r.nodes[v]
	r.Stats.Received++
	if n.seen[m.ID] {
		r.Stats.Duplicates++
		n.dups++
		return
	}
	n.seen[m.ID] = true
	delete(n.requested, m.ID)
	// IDONTWANT goes out on first receipt, before validation (GossipSub v1.2).
	if r.p.IDontWant && m.Size >= r.p.IDontWantMinSize {
		for _, p := range n.mesh {
			if p == from {
				continue
			}
			r.Stats.IDontWant++
			id := m.ID
			r.sendControl(v, p, 1, func() { r.onIDontWant(p, v, id) })
		}
	}
	r.sim.After(r.p.ValidationDelay, func() {
		res := Accept
		if r.hooks.Validate != nil {
			res = r.hooks.Validate(v, m)
		}
		switch res {
		case Ignore:
			r.Stats.Ignored++
			return
		case Reject:
			r.Stats.Rejected++
			return
		}
		r.Stats.Accepted++
		r.remember(v, m)
		if r.hooks.Deliver != nil {
			r.hooks.Deliver(v, m)
		}
		for _, p := range n.mesh {
			if p == from {
				continue
			}
			if n.dontWant[p][m.ID] {
				r.Stats.SkippedByIDW++
				continue
			}
			r.Stats.Forwarded++
			r.sendMessage(v, p, m)
		}
	})
}

func (r *Router) onIDontWant(v, from int, id MessageID) {
	n := r.nodes[v]
	if n.dontWant[from] == nil {
		n.dontWant[from] = map[MessageID]bool{}
	}
	n.dontWant[from][id] = true
}

func (r *Router) heartbeat(v int) {
	n := r.nodes[v]
	r.emitGossip(v)
	// Shift the message cache.
	n.windows = append([][]MessageID{nil}, n.windows...)
	if len(n.windows) > r.p.HistoryLength {
		for _, id := range n.windows[r.p.HistoryLength] {
			delete(n.cache, id)
		}
		n.windows = n.windows[:r.p.HistoryLength]
	}
}

func (r *Router) emitGossip(v int) {
	n := r.nodes[v]
	var ids []MessageID
	for i := 0; i < r.p.HistoryGossip && i < len(n.windows); i++ {
		ids = append(ids, n.windows[i]...)
	}
	if len(ids) == 0 {
		return
	}
	var cand []int
	for _, p := range n.peers {
		if !slices.Contains(n.mesh, p) {
			cand = append(cand, p)
		}
	}
	target := max(r.p.DLazy, int(r.p.GossipFactor*float64(len(cand))))
	if target < len(cand) {
		r.rng.Shuffle(len(cand), func(i, j int) { cand[i], cand[j] = cand[j], cand[i] })
		cand = cand[:target]
	}
	for _, p := range cand {
		r.Stats.IHave++
		r.sendControl(v, p, len(ids), func() { r.onIHave(p, v, ids) })
	}
}

func (r *Router) onIHave(v, from int, ids []MessageID) {
	n := r.nodes[v]
	var want []MessageID
	for _, id := range ids {
		if !n.seen[id] && !n.requested[id] {
			want = append(want, id)
		}
	}
	if len(want) == 0 {
		return
	}
	for _, id := range want {
		n.requested[id] = true
		r.sim.After(r.p.IWantFollowup, func() { delete(n.requested, id) })
	}
	r.Stats.IWant++
	r.sendControl(v, from, len(want), func() { r.onIWant(from, v, want) })
}

func (r *Router) onIWant(v, from int, ids []MessageID) {
	n := r.nodes[v]
	for _, id := range ids {
		if m, ok := n.cache[id]; ok {
			r.Stats.ServedByIWant++
			r.sendMessage(v, from, m)
		}
	}
}
