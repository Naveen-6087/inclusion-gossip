package network

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/Naveen-6087/inclusion-gossip/sim"
)

// Region groups nodes that share base latencies to other regions.
type Region struct {
	Name  string  `json:"name"`
	Share float64 `json:"share"`
}

// DefaultRegions is a rough split of Ethereum nodes by continent. It is a
// modelling assumption, not a measurement.
var DefaultRegions = []Region{
	{"north-america", 0.35},
	{"europe", 0.42},
	{"asia", 0.15},
	{"oceania", 0.03},
	{"south-america", 0.03},
	{"africa", 0.02},
}

// DefaultLatencyMs holds one-way base latencies between DefaultRegions in
// milliseconds, roughly half of typical public round-trip times.
var DefaultLatencyMs = [][]float64{
	{20, 45, 90, 80, 60, 110},
	{45, 15, 85, 140, 100, 70},
	{90, 85, 35, 60, 150, 120},
	{80, 140, 60, 15, 160, 170},
	{60, 100, 150, 160, 30, 170},
	{110, 70, 120, 170, 170, 40},
}

type Config struct {
	Nodes   int         `json:"nodes"`
	Peers   int         `json:"peers"` // target connections per node
	Regions []Region    `json:"regions,omitempty"`
	Latency [][]float64 `json:"latency_ms,omitempty"`
	// Each link's base latency is scaled by a fixed factor drawn from
	// [1-LinkSpread, 1+LinkSpread]; each message adds up to Jitter more.
	LinkSpread float64 `json:"link_spread"`
	JitterMs   float64 `json:"jitter_ms"`
	UploadMbps float64 `json:"upload_mbps"`
	// A fraction of nodes can have a slower uplink.
	SlowFraction   float64 `json:"slow_fraction"`
	SlowUploadMbps float64 `json:"slow_upload_mbps"`
	Loss           float64 `json:"loss"` // per-message drop probability
}

func (c Config) WithDefaults() Config {
	if c.Peers == 0 {
		c.Peers = 50
	}
	if len(c.Regions) == 0 {
		c.Regions = DefaultRegions
		if c.Latency == nil {
			c.Latency = DefaultLatencyMs
		}
	}
	if c.UploadMbps == 0 {
		c.UploadMbps = 50
	}
	if c.SlowUploadMbps == 0 {
		c.SlowUploadMbps = 10
	}
	return c
}

func (c Config) Validate() error {
	switch {
	case c.Nodes < 2:
		return errors.New("network: need at least 2 nodes")
	case c.Peers < 1 || c.Peers >= c.Nodes:
		return fmt.Errorf("network: peers must be in [1, nodes-1], got %d", c.Peers)
	case len(c.Latency) != len(c.Regions):
		return errors.New("network: latency matrix does not match regions")
	case c.Loss < 0 || c.Loss >= 1:
		return errors.New("network: loss must be in [0, 1)")
	case c.JitterMs < 0:
		return errors.New("network: jitter_ms must not be negative")
	case c.LinkSpread < 0 || c.LinkSpread >= 1:
		return errors.New("network: link_spread must be in [0, 1)")
	case c.SlowFraction < 0 || c.SlowFraction > 1:
		return errors.New("network: slow_fraction must be in [0, 1]")
	case c.UploadMbps <= 0 || c.SlowUploadMbps <= 0:
		return errors.New("network: upload bandwidth must be positive")
	}
	for i, row := range c.Latency {
		if len(row) != len(c.Regions) {
			return fmt.Errorf("network: latency row %d has %d entries", i, len(row))
		}
		for j, v := range row {
			if v < 0 || v != c.Latency[j][i] {
				return fmt.Errorf("network: latency matrix must be symmetric and non-negative (%d,%d)", i, j)
			}
		}
	}
	return nil
}

type link struct {
	peer    int
	latency time.Duration
}

// Network is a fixed connection graph with per-link latencies and per-node
// upload bandwidth.
type Network struct {
	cfg    Config
	region []int
	upload []float64 // bytes per second
	links  [][]link
}

// Build creates a connection graph in which every node has about cfg.Peers
// connections, joined into one component.
func Build(cfg Config, rng *rand.Rand) (*Network, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	n := &Network{
		cfg:    cfg,
		region: make([]int, cfg.Nodes),
		upload: make([]float64, cfg.Nodes),
		links:  make([][]link, cfg.Nodes),
	}
	total := 0.0
	for _, r := range cfg.Regions {
		total += r.Share
	}
	for i := range cfg.Nodes {
		x := rng.Float64() * total
		for r, reg := range cfg.Regions {
			if x < reg.Share || r == len(cfg.Regions)-1 {
				n.region[i] = r
				break
			}
			x -= reg.Share
		}
		mbps := cfg.UploadMbps
		if rng.Float64() < cfg.SlowFraction {
			mbps = cfg.SlowUploadMbps
		}
		n.upload[i] = mbps * 1e6 / 8
	}

	connected := make([]map[int]bool, cfg.Nodes)
	for i := range connected {
		connected[i] = map[int]bool{}
	}
	addEdge := func(a, b int) {
		base := cfg.Latency[n.region[a]][n.region[b]]
		f := 1 + cfg.LinkSpread*(2*rng.Float64()-1)
		lat := time.Duration(base * f * float64(time.Millisecond))
		connected[a][b], connected[b][a] = true, true
		n.links[a] = append(n.links[a], link{b, lat})
		n.links[b] = append(n.links[b], link{a, lat})
	}
	// Random pairing of connection slots gives a near-regular graph.
	stubs := make([]int, 0, cfg.Nodes*cfg.Peers)
	for i := range cfg.Nodes {
		for range cfg.Peers {
			stubs = append(stubs, i)
		}
	}
	rng.Shuffle(len(stubs), func(i, j int) { stubs[i], stubs[j] = stubs[j], stubs[i] })
	for i := 0; i+1 < len(stubs); i += 2 {
		a, b := stubs[i], stubs[i+1]
		if a != b && !connected[a][b] && len(n.links[a]) < cfg.Peers && len(n.links[b]) < cfg.Peers {
			addEdge(a, b)
		}
	}
	// Join any separate components to the first one.
	comp := n.components()
	for c := 1; c < len(comp); c++ {
		a := comp[0][rng.IntN(len(comp[0]))]
		b := comp[c][rng.IntN(len(comp[c]))]
		addEdge(a, b)
	}
	return n, nil
}

// FromEdges builds a network with the given links, all with the same
// latency. It is meant for small hand-made topologies.
func FromEdges(nodes int, edges [][2]int, latency time.Duration, uploadMbps float64) *Network {
	n := &Network{
		cfg:    Config{Nodes: nodes, Regions: []Region{{"lab", 1}}, UploadMbps: uploadMbps},
		region: make([]int, nodes),
		upload: make([]float64, nodes),
		links:  make([][]link, nodes),
	}
	for i := range n.upload {
		n.upload[i] = uploadMbps * 1e6 / 8
	}
	for _, e := range edges {
		n.links[e[0]] = append(n.links[e[0]], link{e[1], latency})
		n.links[e[1]] = append(n.links[e[1]], link{e[0], latency})
	}
	return n
}

func (n *Network) components() [][]int {
	seen := make([]bool, len(n.links))
	var out [][]int
	for s := range n.links {
		if seen[s] {
			continue
		}
		var comp []int
		stack := []int{s}
		seen[s] = true
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp = append(comp, v)
			for _, l := range n.links[v] {
				if !seen[l.peer] {
					seen[l.peer] = true
					stack = append(stack, l.peer)
				}
			}
		}
		out = append(out, comp)
	}
	return out
}

func (n *Network) Size() int { return len(n.links) }

// Peers returns the nodes connected to v, in a fixed order.
func (n *Network) Peers(v int) []int {
	out := make([]int, len(n.links[v]))
	for i, l := range n.links[v] {
		out[i] = l.peer
	}
	return out
}

func (n *Network) Region(v int) string { return n.cfg.Regions[n.region[v]].Name }

// Connected reports whether the graph is a single component.
func (n *Network) Connected() bool { return len(n.components()) == 1 }

func (n *Network) latency(a, b int) (time.Duration, bool) {
	for _, l := range n.links[a] {
		if l.peer == b {
			return l.latency, true
		}
	}
	return 0, false
}

// Transport delivers messages over a Network inside a simulation. Each node
// has one uplink: messages leave it one after another, so a node that sends
// many copies of a large message delays the later copies.
type Transport struct {
	net       *Network
	sim       *sim.Sim
	rng       *rand.Rand
	freeAt    []sim.Time
	BytesSent []int64
	Sent      []int64
	Dropped   int64
}

func NewTransport(net *Network, s *sim.Sim) *Transport {
	return &Transport{
		net:       net,
		sim:       s,
		rng:       s.Rand("transport"),
		freeAt:    make([]sim.Time, net.Size()),
		BytesSent: make([]int64, net.Size()),
		Sent:      make([]int64, net.Size()),
	}
}

// Send queues size bytes from one node to a connected peer and calls deliver
// when they arrive. Lost messages still use the sender's uplink.
func (t *Transport) Send(from, to, size int, deliver func()) {
	lat, ok := t.net.latency(from, to)
	if !ok {
		panic(fmt.Sprintf("network: %d and %d are not connected", from, to))
	}
	now := t.sim.Now()
	start := max(now, t.freeAt[from])
	tx := time.Duration(float64(size) / t.net.upload[from] * float64(time.Second))
	t.freeAt[from] = start + tx
	t.BytesSent[from] += int64(size)
	t.Sent[from]++
	if t.net.cfg.Loss > 0 && t.rng.Float64() < t.net.cfg.Loss {
		t.Dropped++
		return
	}
	var jitter time.Duration
	if t.net.cfg.JitterMs > 0 {
		jitter = time.Duration(t.rng.Float64() * t.net.cfg.JitterMs * float64(time.Millisecond))
	}
	t.sim.At(start+tx+lat+jitter, deliver)
}
