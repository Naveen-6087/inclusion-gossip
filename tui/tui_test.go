package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Naveen-6087/inclusion-gossip/experiment"
)

func TestSparklineBarResample(t *testing.T) {
	if got := Sparkline([]float64{0, 0.5, 1}); got != " ▅█" {
		t.Fatalf("sparkline %q", got)
	}
	if Bar(0.5, 4) != "██░░" || Bar(2, 2) != "██" || Bar(-1, 2) != "░░" {
		t.Fatal("bar")
	}
	if got := Resample([]float64{1, 2, 3, 4}, 2); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("resample %v", got)
	}
	if Resample(nil, 5) != nil {
		t.Fatal("resample of nothing")
	}
}

func entries(t *testing.T) []Entry {
	t.Helper()
	es, err := LoadEntries([]string{"../experiments/baseline.json", "../experiments/late-publisher.json"})
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestLoadEntries(t *testing.T) {
	es := entries(t)
	if len(es) != 2 || es[0].Name == "" || len(es[1].Points) < 2 {
		t.Fatalf("entries %+v", es)
	}
	if _, err := LoadEntries([]string{"missing.json"}); err == nil {
		t.Fatal("missing file accepted")
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestModelNavigationAndStaleResults(t *testing.T) {
	es := entries(t)
	m := New([]Entry{es[1], es[0]}, 1)
	calls := 0
	m.runSlot = func(c experiment.Config, seed uint64) (experiment.RunResult, error) {
		calls++
		return experiment.RunResult{Seed: seed, ProposerLists: 16}, nil
	}
	first := m.Init()
	if !m.running || m.gen != 1 {
		t.Fatal("Init did not start a run")
	}
	step := func(k string) tea.Cmd {
		next, cmd := m.Update(key(k))
		m = next.(Model)
		return cmd
	}
	step("right")
	if m.point != 1 {
		t.Fatalf("point %d", m.point)
	}
	// The first run finishes after the point changed: its result is stale.
	next, _ := m.Update(first())
	m = next.(Model)
	if m.result != nil || !m.running {
		t.Fatal("stale result was shown")
	}
	cmd := step("s")
	if m.seed != 2 {
		t.Fatalf("seed %d", m.seed)
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.running || m.result == nil || m.result.Seed != 2 {
		t.Fatalf("result not stored: %+v", m.result)
	}
	step("down")
	if m.selected != 1 || m.point != 0 {
		t.Fatalf("selected %d point %d", m.selected, m.point)
	}
	step("down")
	step("up")
	step("up")
	if m.selected != 0 {
		t.Fatalf("wrap-around selected %d", m.selected)
	}
	step("S")
	step("S")
	if m.seed != 1 {
		t.Fatalf("seed went below 1: %d", m.seed)
	}
	step("j")
	step("k")
	step("k")
	if m.offset != 0 {
		t.Fatalf("offset %d", m.offset)
	}
	if _, ok := step("q")().(tea.QuitMsg); !ok {
		t.Fatal("q did not quit")
	}
	if calls != 2 {
		t.Fatalf("%d runs executed, want 2", calls)
	}
}

func TestRenderRealSlot(t *testing.T) {
	es := entries(t)
	p := es[1].Points[len(es[1].Points)-1]
	p.Config.Network.Nodes = 300
	p.Config.Network.Peers = 20
	r, err := experiment.RunSlot(p.Config, 1)
	if err != nil {
		t.Fatal(err)
	}
	f := Frame{
		Experiments: []string{es[0].Name, es[1].Name}, Selected: 1, Name: es[1].Name, Point: p.Label,
		Points: len(es[1].Points), Seed: 1, Timeline: p.Config.Timeline, Result: &r, Elapsed: 900 * time.Millisecond,
	}
	out := Render(f, 160, 50)
	for _, want := range []string{"ilsim", es[1].Name, "Share of nodes holding a list", "honest", "adversary",
		"freeze", "Next block", "Traffic", "Inclusion lists", "late", "done"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	f.Result, f.Running = nil, true
	if out := Render(f, 120, 40); !strings.Contains(out, "simulating") || !strings.Contains(out, "waiting for the first run") {
		t.Fatal("running state not shown")
	}
	f.Running, f.Err = false, errors.New("boom")
	if !strings.Contains(Render(f, 120, 40), "boom") {
		t.Fatal("error not shown")
	}
}
