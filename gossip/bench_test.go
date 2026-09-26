package gossip

import (
	"testing"
	"time"
)

// BenchmarkPropagate1000 floods one 4 KB message through 1,000 nodes with
// heartbeats running.
func BenchmarkPropagate1000(b *testing.B) {
	net := randomNet(b, 1000, 50, 0, 1)
	for b.Loop() {
		h := newHarness(b, net, DefaultParams(), nil)
		h.r.Start(3 * time.Second)
		h.r.Publish(0, &Message{ID: 1, Size: 4000}, nil)
		h.sim.Run(3 * time.Second)
	}
}
