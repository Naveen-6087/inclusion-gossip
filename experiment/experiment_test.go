package experiment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/Naveen-6087/inclusion-gossip/network"
)

func mustParse(t *testing.T, s string) Config {
	t.Helper()
	c, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func small(t *testing.T, extra string) Config {
	return mustParse(t, `{"name": "t", "runs": 4, "network": {"nodes": 200, "peers": 20}`+extra+`}`)
}

func TestParseAndDefaults(t *testing.T) {
	c := mustParse(t, `{"name": "x", "honest": {"at": "7.5s"}, "adversaries": [{"kind": "equivocate", "at": "8s"}]}`).WithDefaults()
	if c.Runs != 20 || c.Seed != 1 || c.Network.Nodes != 1000 || c.Honest.At.Duration != 7500*time.Millisecond {
		t.Fatalf("defaults %+v", c)
	}
	a := c.Adversaries[0]
	if a.N() != 1 || a.Split != 0.5 || a.SecondAt.Duration != 8*time.Second {
		t.Fatalf("adversary defaults %+v", a)
	}
	if c.Timeline.Freeze.Duration != 9*time.Second || c.Timeline.ProposerCutoff.Duration != 11*time.Second || c.Timeline.ProposerView != "cutoff" {
		t.Fatalf("timeline %+v", c.Timeline)
	}
	if _, err := Parse([]byte(`{"name": "x", "netwrok": {}}`)); err == nil {
		t.Error("unknown field accepted")
	}
	if _, err := Parse([]byte(`{"name": "x", "honest": {"at": 8}}`)); err == nil {
		t.Error("numeric duration accepted")
	}
}

func TestValidate(t *testing.T) {
	bad := []string{
		`{"runs": 1}`,
		`{"name": "x", "network": {"nodes": 10}}`,
		`{"name": "x", "timeline": {"freeze": "12s"}}`,
		`{"name": "x", "timeline": {"proposer_view": "later"}}`,
		`{"name": "x", "adversaries": [{"kind": "sneaky"}]}`,
		`{"name": "x", "adversaries": [{"kind": "late", "count": 17}]}`,
		`{"name": "x", "adversaries": [{"kind": "equivocate", "at": "9s", "second_at": "8s"}]}`,
		`{"name": "x", "mempool": {"max_il_bytes": 9000}}`,
	}
	for _, s := range bad {
		if _, err := mustParse(t, s).Expand(); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
}

func TestSweepExpansion(t *testing.T) {
	c := mustParse(t, `{"name": "s", "adversaries": [{"kind": "late", "at": "8s"}],
		"sweep": {"path": "adversaries.0.at", "values": ["8.5s", "9s"]}}`)
	pts, err := c.Expand()
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 || pts[0].Label != "8.5s" || pts[1].Config.Adversaries[0].At.Duration != 9*time.Second {
		t.Fatalf("points %+v", pts)
	}
	if pts[0].Config.Sweep != nil {
		t.Error("sweep left in point config")
	}

	c = mustParse(t, `{"name": "s", "sweep": {"path": "network.loss", "values": [0.1]}}`)
	if pts, err := c.Expand(); err != nil || pts[0].Config.Network.Loss != 0.1 {
		t.Fatalf("network.loss: %v", err)
	}
	c = mustParse(t, `{"name": "s", "sweep": {"path": "adversaries.3.at", "values": ["1s"]}}`)
	if _, err := c.Expand(); err == nil {
		t.Error("out-of-range index accepted")
	}
	c = mustParse(t, `{"name": "s", "sweep": {"path": "network.loss", "values": [2]}}`)
	if _, err := c.Expand(); err == nil {
		t.Error("invalid swept value accepted")
	}
}

func TestDeterministicAcrossWorkers(t *testing.T) {
	c := mustParse(t, `{"name": "t", "runs": 4, "network": {"nodes": 200, "peers": 20, "loss": 0.02},
		"adversaries": [{"kind": "equivocate", "at": "8.5s", "second_at": "9.2s"}]}`)
	encode := func(workers int) []byte {
		res, err := Run(c, workers, true)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(res)
		return b
	}
	a, b := encode(1), encode(8)
	if !bytes.Equal(a, b) {
		t.Fatal("results depend on the number of workers")
	}
	c.Seed = 2
	if bytes.Equal(a, encode(4)) {
		t.Fatal("different seeds gave identical results")
	}
}

func TestHonestBaseline(t *testing.T) {
	res, err := Run(small(t, ""), 0, true)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summaries[0]
	if s.HonestAtFreeze.Min != 1 || s.TxUnsatisfied.Max != 0 || s.ListMismatch.Max != 0 || s.ProposerLists.Min != 16 {
		t.Fatalf("honest baseline %+v", s)
	}
	if s.HonestP50Ms.Mean <= 0 || s.HonestP99Ms.Mean < s.HonestP50Ms.Mean || s.AdversaryAtFreeze != nil {
		t.Fatalf("latencies %+v", s)
	}
	for _, r := range res.Runs[0] {
		if r.MeanLists != 16 || r.Attesters != 199 {
			t.Fatalf("run %+v", r)
		}
	}
}

func TestLateListsAndProposerCutoff(t *testing.T) {
	// A list published after the freeze deadline is in nobody's attester
	// view, but the proposer still counts it before its cutoff.
	res, _ := Run(small(t, `, "adversaries": [{"kind": "late", "at": "9.2s"}]`), 0, false)
	s := res.Summaries[0]
	if s.AdversaryAtFreeze.Max != 0 || s.AdversaryInViews.Max != 0 || s.AdversaryProposer.Min != 1 || s.TxUnsatisfied.Max != 0 {
		t.Fatalf("late after freeze: %+v", s)
	}
	// With the cutoff view the proposer's lists are a superset of every
	// attester's, wherever the late list lands.
	for _, at := range []string{"8.8s", "8.85s", "8.9s", "8.95s"} {
		res, _ := Run(small(t, `, "adversaries": [{"kind": "late", "at": "`+at+`"}]`), 0, false)
		if m := res.Summaries[0].ListMismatch.Max; m != 0 {
			t.Errorf("late at %s: mismatch %.3f with proposer cutoff", at, m)
		}
	}
}

func TestStrictProposerViewCanDisagree(t *testing.T) {
	c := small(t, `, "runs": 12, "timeline": {"proposer_view": "freeze"}, "adversaries": [{"kind": "late", "at": "8.85s"}]`)
	res, err := Run(c, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summaries[0]
	if s.ListMismatch.Max == 0 {
		t.Fatalf("no disagreement for a list landing at the freeze deadline: %+v", s)
	}
	if s.TxUnsatisfied.Max > s.ListMismatch.Max {
		t.Fatal("transaction-level disagreement exceeds list-level")
	}
}

func TestEquivocation(t *testing.T) {
	res, err := Run(small(t, `, "adversaries": [{"kind": "equivocate", "at": "8s"}]`), 0, true)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summaries[0]
	if s.EquivDetectCheck.Min != 1 || s.AdversaryInViews.Max != 0 || s.AdversaryProposer.Max != 0 {
		t.Fatalf("simultaneous equivocation: %+v", s)
	}
	if s.EquivDetectFreeze.Mean < 0.99 {
		t.Fatalf("equivocation evidence did not spread before the freeze: %+v", *s.EquivDetectFreeze)
	}
	// Version B after the proposer's cutoff: the proposer keeps counting the
	// list, attesters drop it, which is harmless (their view shrinks).
	res, _ = Run(small(t, `, "adversaries": [{"kind": "equivocate", "at": "8s", "second_at": "11.5s"}]`), 0, false)
	s = res.Summaries[0]
	if s.AdversaryProposer.Min != 1 || s.EquivDetectCheck.Min < 0.99 || s.TxUnsatisfied.Max != 0 {
		t.Fatalf("late second version: %+v", s)
	}
}

// Without forwarding the second list, nodes that saw only one version keep
// counting it and views split.
func TestEquivocationWithoutEvidence(t *testing.T) {
	res, err := Run(small(t, `, "rules": {"lists_per_validator": 1}, "adversaries": [{"kind": "equivocate", "at": "8s"}]`), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summaries[0]
	if s.EquivDetectCheck.Max > 0.2 || s.TxUnsatisfied.Max < 0.2 {
		t.Fatalf("expected split views without evidence: detected %.2f, unsatisfied %.2f", s.EquivDetectCheck.Max, s.TxUnsatisfied.Max)
	}
}

func TestSilentAndInvalid(t *testing.T) {
	res, err := Run(small(t, `, "adversaries": [{"kind": "silent", "count": 3}, {"kind": "invalid", "at": "8s"}]`), 0, true)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Summaries[0]
	if s.ProposerLists.Max != 12 {
		t.Fatalf("proposer lists %+v", s.ProposerLists)
	}
	// An invalid list is rejected by every neighbour and goes no further.
	if s.AdversaryAtFreeze.Max != 0 || s.AdversaryInViews.Max != 0 {
		t.Fatalf("invalid list spread: %+v", s)
	}
	for _, r := range res.Runs[0] {
		if r.Gossip.Rejected == 0 {
			t.Fatal("no rejections recorded")
		}
	}
}

// Invariants that must hold for any configuration.
func TestRandomConfigsInvariants(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 42))
	kinds := []string{KindLate, KindSilent, KindInvalid, KindEquivocate}
	for i := range 25 {
		nodes := 40 + rng.IntN(120)
		cfg := Config{
			Name: fmt.Sprintf("random-%d", i), Runs: 2, Seed: rng.Uint64N(1000) + 1,
			Network: network.Config{Nodes: nodes, Peers: 5 + rng.IntN(20), Loss: rng.Float64() * 0.2, SlowFraction: rng.Float64()},
		}
		if rng.IntN(2) == 0 {
			cfg.Timeline.ProposerView = "freeze"
		}
		for range rng.IntN(4) {
			at := dur(time.Duration(7000+rng.IntN(5000)) * time.Millisecond)
			n := 1 + rng.IntN(3)
			a := Adversary{Kind: kinds[rng.IntN(len(kinds))], Count: &n, At: at}
			if a.Kind == KindEquivocate {
				a.SecondAt = dur(at.Duration + time.Duration(rng.IntN(3000))*time.Millisecond)
			}
			cfg.Adversaries = append(cfg.Adversaries, a)
		}
		res, err := Run(cfg, 0, true)
		if err != nil {
			t.Fatalf("config %d: %v", i, err)
		}
		for _, r := range res.Runs[0] {
			if r.TxUnsatisfied > r.ListMismatch || r.ProposerLists > 16 || r.Events == 0 {
				t.Fatalf("config %d: %+v", i, r)
			}
			for _, l := range r.Lists {
				if !(0 <= l.AtFreeze && l.AtFreeze <= l.AtCutoff && l.AtCutoff <= l.Final && l.Final <= 1) {
					t.Fatalf("config %d: coverage not monotone %+v", i, l)
				}
				if l.Kind == KindSilent && l.Final != 0 || l.Kind == KindInvalid && l.Final != 0 {
					t.Fatalf("config %d: %s list reached nodes", i, l.Kind)
				}
				if l.P50Ms > l.P99Ms || l.P99Ms > l.MaxMs {
					t.Fatalf("config %d: quantiles %+v", i, l)
				}
			}
		}
	}
}

func TestOutputs(t *testing.T) {
	c := small(t, `, "runs": 2, "adversaries": [{"kind": "late", "at": "8s"}], "sweep": {"path": "adversaries.0.at", "values": ["8s", "9.5s"]}`)
	res, err := Run(c, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	var table, csv bytes.Buffer
	if err := res.PrintTable(&table); err != nil {
		t.Fatal(err)
	}
	if err := res.WriteCSV(&csv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table.String(), "9.5s") || strings.Count(csv.String(), "\n") != 3 {
		t.Fatalf("table:\n%s\ncsv:\n%s", table.String(), csv.String())
	}
	b, err := json.Marshal(res)
	if err != nil || !strings.Contains(string(b), `"summaries"`) || strings.Contains(string(b), `"runs":[[`) {
		t.Fatalf("json: %v", err)
	}
}
