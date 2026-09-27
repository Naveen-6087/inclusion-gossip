package experiment

import "testing"

func BenchmarkSlot1000Nodes(b *testing.B) {
	c := Config{Name: "bench"}.WithDefaults()
	seed := uint64(0)
	for b.Loop() {
		seed++
		if _, err := RunSlot(c, seed); err != nil {
			b.Fatal(err)
		}
	}
}
