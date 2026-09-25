package network

import (
	"testing"
	"time"

	"github.com/Naveen-6087/inclusion-gossip/sim"
)

func TestBuildIsConnectedAndNearRegular(t *testing.T) {
	for seed := uint64(1); seed <= 5; seed++ {
		n, err := Build(Config{Nodes: 500, Peers: 20}, sim.Stream(seed, "topology"))
		if err != nil {
			t.Fatal(err)
		}
		if !n.Connected() {
			t.Fatalf("seed %d: graph not connected", seed)
		}
		total, low := 0, 0
		for v := range n.Size() {
			d := len(n.Peers(v))
			total += d
			if d < 15 {
				low++
			}
			for _, p := range n.Peers(v) {
				if p == v {
					t.Fatal("self loop")
				}
				if _, ok := n.latency(p, v); !ok {
					t.Fatal("asymmetric link")
				}
			}
		}
		if mean := float64(total) / 500; mean < 18 || mean > 20.1 {
			t.Errorf("seed %d: mean degree %.2f", seed, mean)
		}
		if low > 10 {
			t.Errorf("seed %d: %d nodes with fewer than 15 peers", seed, low)
		}
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	a, _ := Build(Config{Nodes: 200, Peers: 10}, sim.Stream(3, "topology"))
	b, _ := Build(Config{Nodes: 200, Peers: 10}, sim.Stream(3, "topology"))
	for v := range a.Size() {
		pa, pb := a.Peers(v), b.Peers(v)
		if len(pa) != len(pb) || a.Region(v) != b.Region(v) {
			t.Fatal("same seed, different graph")
		}
		for i := range pa {
			if pa[i] != pb[i] {
				t.Fatal("same seed, different graph")
			}
		}
	}
}

func TestValidate(t *testing.T) {
	bad := []Config{
		{Nodes: 1, Peers: 1},
		{Nodes: 10, Peers: 10},
		{Nodes: 10, Peers: 3, Loss: 1},
		{Nodes: 10, Peers: 3, Regions: []Region{{"a", 1}}, Latency: [][]float64{{1, 2}}},
		{Nodes: 10, Peers: 3, Regions: []Region{{"a", 1}, {"b", 1}}, Latency: [][]float64{{1, 2}, {3, 1}}},
	}
	for i, c := range bad {
		if _, err := Build(c, sim.Stream(1, "x")); err == nil {
			t.Errorf("config %d accepted", i)
		}
	}
}

// Two nodes, one region with 10 ms latency and an 8 Mbit/s uplink: a 1000
// byte message takes 1 ms to send, so back-to-back copies arrive 1 ms apart.
func TestTransportTiming(t *testing.T) {
	cfg := Config{
		Nodes: 3, Peers: 2,
		Regions:    []Region{{"only", 1}},
		Latency:    [][]float64{{10}},
		UploadMbps: 8,
	}
	n, err := Build(cfg, sim.Stream(1, "t"))
	if err != nil {
		t.Fatal(err)
	}
	s := sim.New(1)
	tr := NewTransport(n, s)
	var arrivals []time.Duration
	for range 3 {
		tr.Send(0, n.Peers(0)[0], 1000, func() { arrivals = append(arrivals, s.Now()) })
	}
	s.Run(time.Second)
	want := []time.Duration{11 * time.Millisecond, 12 * time.Millisecond, 13 * time.Millisecond}
	for i := range want {
		if arrivals[i] != want[i] {
			t.Fatalf("arrivals %v, want %v", arrivals, want)
		}
	}
	if tr.BytesSent[0] != 3000 || tr.Sent[0] != 3 {
		t.Fatalf("accounting %d bytes %d msgs", tr.BytesSent[0], tr.Sent[0])
	}
}

func TestTransportLoss(t *testing.T) {
	cfg := Config{Nodes: 2, Peers: 1, Regions: []Region{{"only", 1}}, Latency: [][]float64{{5}}, Loss: 0.3}
	n, _ := Build(cfg, sim.Stream(1, "t"))
	s := sim.New(9)
	tr := NewTransport(n, s)
	got := 0
	for range 10000 {
		tr.Send(0, 1, 10, func() { got++ })
	}
	s.Run(time.Hour)
	if frac := float64(got) / 10000; frac < 0.67 || frac > 0.73 {
		t.Fatalf("delivered %.3f with 30%% loss", frac)
	}
	if int64(got)+tr.Dropped != 10000 {
		t.Fatal("drops not counted")
	}
}
