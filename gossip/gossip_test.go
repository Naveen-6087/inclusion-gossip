package gossip

import (
	"testing"
	"time"

	"github.com/Naveen-6087/inclusion-gossip/network"
	"github.com/Naveen-6087/inclusion-gossip/sim"
)

type harness struct {
	sim       *sim.Sim
	tr        *network.Transport
	r         *Router
	delivered map[int][]time.Duration
}

func newHarness(t testing.TB, net *network.Network, p Params, validate func(int, *Message) Result) *harness {
	t.Helper()
	h := &harness{sim: sim.New(1), delivered: map[int][]time.Duration{}}
	h.tr = network.NewTransport(net, h.sim)
	h.r = New(h.sim, net, h.tr, p, Hooks{
		Validate: validate,
		Deliver:  func(v int, m *Message) { h.delivered[v] = append(h.delivered[v], h.sim.Now()) },
	})
	return h
}

func line(n int) [][2]int {
	var e [][2]int
	for i := 0; i+1 < n; i++ {
		e = append(e, [2]int{i, i + 1})
	}
	return e
}

// On a line with 10 ms links, 8 Mbit/s uplinks and 2 ms validation, a 960
// byte message (1000 on the wire) needs 1 + 10 + 2 = 13 ms per hop.
func TestHopTiming(t *testing.T) {
	net := network.FromEdges(5, line(5), 10*time.Millisecond, 8)
	p := DefaultParams()
	p.IDontWant = false
	h := newHarness(t, net, p, nil)
	h.r.Publish(0, &Message{ID: 1, Size: 960}, nil)
	h.sim.Run(time.Second)
	for v := 1; v < 5; v++ {
		want := time.Duration(v) * 13 * time.Millisecond
		if got := h.delivered[v]; len(got) != 1 || got[0] != want {
			t.Errorf("node %d delivered at %v, want [%v]", v, got, want)
		}
	}
}

func TestRejectStopsPropagation(t *testing.T) {
	net := network.FromEdges(4, line(4), time.Millisecond, 100)
	h := newHarness(t, net, DefaultParams(), func(v int, m *Message) Result {
		if v == 2 {
			return Reject
		}
		return Accept
	})
	h.r.Publish(0, &Message{ID: 7, Size: 100}, nil)
	h.sim.Run(time.Second)
	if len(h.delivered[1]) != 1 || len(h.delivered[2]) != 0 || len(h.delivered[3]) != 0 {
		t.Fatalf("delivered %v", h.delivered)
	}
	if h.r.Stats.Rejected != 1 {
		t.Fatalf("rejected %d", h.r.Stats.Rejected)
	}
}

// Node 2 is connected to node 1 but outside its mesh (D=1), so it can only
// learn the message through IHAVE/IWANT at node 1's next heartbeat.
func TestLazyGossipRecovers(t *testing.T) {
	net := network.FromEdges(3, [][2]int{{0, 1}, {1, 2}}, 10*time.Millisecond, 100)
	p := DefaultParams()
	p.D, p.DLow, p.DHigh = 1, 1, 1
	p.FloodPublish = false
	h := newHarness(t, net, p, nil)
	if len(h.r.Mesh(2)) != 0 && len(h.r.Mesh(0)) != 0 {
		t.Skip("mesh did not leave a lazy link with this seed")
	}
	src := 0
	if len(h.r.Mesh(0)) == 0 {
		src = 2
	}
	dst := 2 - src
	h.r.Start(10 * time.Second)
	h.r.Publish(src, &Message{ID: 9, Size: 200}, nil)
	h.sim.Run(10 * time.Second)
	got := h.delivered[dst]
	if len(got) != 1 {
		t.Fatalf("node %d never got the message: %v", dst, h.delivered)
	}
	// Eager push would have taken ~24 ms; gossip needs a heartbeat plus an
	// IHAVE, IWANT and message round trip.
	if got[0] < 30*time.Millisecond || h.r.Stats.IWant == 0 || h.r.Stats.ServedByIWant == 0 {
		t.Fatalf("delivered at %v via IWANT=%d served=%d", got[0], h.r.Stats.IWant, h.r.Stats.ServedByIWant)
	}
}

func randomNet(t testing.TB, nodes, peers int, loss float64, seed uint64) *network.Network {
	t.Helper()
	net, err := network.Build(network.Config{Nodes: nodes, Peers: peers, Loss: loss}, sim.Stream(seed, "topology"))
	if err != nil {
		t.Fatal(err)
	}
	return net
}

func TestFullPropagationExactlyOnce(t *testing.T) {
	net := randomNet(t, 400, 30, 0, 1)
	h := newHarness(t, net, DefaultParams(), nil)
	h.r.Start(5 * time.Second)
	h.r.Publish(17, &Message{ID: 1, Size: 4000}, nil)
	h.sim.Run(5 * time.Second)
	for v := range net.Size() {
		if len(h.delivered[v]) != 1 {
			t.Fatalf("node %d delivered %d times", v, len(h.delivered[v]))
		}
	}
	low := 0
	for v := range net.Size() {
		if len(h.r.Mesh(v)) < 6 {
			low++
		}
	}
	if low > 8 {
		t.Errorf("%d of 400 nodes have fewer than D_low mesh peers", low)
	}
}

func TestLossIsRepairedByGossip(t *testing.T) {
	net := randomNet(t, 300, 30, 0.2, 2)
	p := DefaultParams()
	p.FloodPublish = false
	h := newHarness(t, net, p, nil)
	h.r.Start(6 * time.Second)
	h.r.Publish(0, &Message{ID: 1, Size: 2000}, nil)
	h.sim.Run(6 * time.Second)
	for v := range net.Size() {
		if len(h.delivered[v]) != 1 {
			t.Fatalf("node %d missing after gossip with 20%% loss", v)
		}
	}
	if h.r.Stats.ServedByIWant == 0 {
		t.Fatal("no message was recovered through IWANT")
	}
}

func TestIDontWantSavesDuplicates(t *testing.T) {
	run := func(idw bool) (dups, sent int64) {
		net := randomNet(t, 400, 30, 0, 3)
		p := DefaultParams()
		p.IDontWant = idw
		h := newHarness(t, net, p, nil)
		h.r.Publish(0, &Message{ID: 1, Size: 8000}, nil)
		h.sim.Run(5 * time.Second)
		for _, b := range h.tr.BytesSent {
			sent += b
		}
		return h.r.Stats.Duplicates, sent
	}
	dupOff, bytesOff := run(false)
	dupOn, bytesOn := run(true)
	if dupOn >= dupOff || bytesOn >= bytesOff {
		t.Fatalf("IDONTWANT on: %d dups / %d bytes, off: %d / %d", dupOn, bytesOn, dupOff, bytesOff)
	}
}

func TestFloodPublish(t *testing.T) {
	net := randomNet(t, 100, 20, 0, 4)
	for _, flood := range []bool{true, false} {
		p := DefaultParams()
		p.FloodPublish = flood
		h := newHarness(t, net, p, nil)
		h.r.Publish(5, &Message{ID: 1, Size: 100}, nil)
		want := int64(len(net.Peers(5)))
		if !flood {
			want = int64(len(h.r.Mesh(5)))
		}
		if h.tr.Sent[5] != want {
			t.Errorf("flood=%v: publisher sent %d messages, want %d", flood, h.tr.Sent[5], want)
		}
	}
}

func TestTargetedPublish(t *testing.T) {
	net := network.FromEdges(5, [][2]int{{0, 1}, {0, 2}, {0, 3}, {0, 4}}, time.Millisecond, 100)
	h := newHarness(t, net, DefaultParams(), nil)
	h.r.Publish(0, &Message{ID: 1, Size: 10}, []int{1, 2})
	h.sim.Run(time.Second)
	if len(h.delivered[1]) != 1 || len(h.delivered[3]) != 0 {
		t.Fatalf("delivered %v", h.delivered)
	}
}

func TestDeterministic(t *testing.T) {
	run := func() Stats {
		net := randomNet(t, 300, 25, 0.05, 5)
		h := newHarness(t, net, DefaultParams(), nil)
		h.r.Start(4 * time.Second)
		for i := range 5 {
			h.r.Publish(i*40, &Message{ID: MessageID(i + 1), Size: 3000}, nil)
		}
		h.sim.Run(4 * time.Second)
		return h.r.Stats
	}
	if a, b := run(), run(); a != b {
		t.Fatalf("same seed, different runs:\n%+v\n%+v", a, b)
	}
}
