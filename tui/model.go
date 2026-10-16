package tui

import (
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Naveen-6087/inclusion-gossip/experiment"
)

// Entry is one experiment file and its sweep points.
type Entry struct {
	Name   string
	Config experiment.Config
	Points []experiment.Point
}

// LoadEntries reads and expands experiment files.
func LoadEntries(paths []string) ([]Entry, error) {
	var out []Entry
	for _, p := range paths {
		cfg, err := experiment.Load(p)
		if err != nil {
			return nil, err
		}
		points, err := cfg.Expand()
		if err != nil {
			return nil, err
		}
		name := cfg.Name
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		}
		out = append(out, Entry{Name: name, Config: cfg, Points: points})
	}
	return out, nil
}

type resultMsg struct {
	gen     int
	r       experiment.RunResult
	err     error
	elapsed time.Duration
}

// Model is the Bubble Tea model of the slot explorer.
type Model struct {
	entries  []Entry
	selected int
	point    int
	seed     uint64
	gen      int
	running  bool
	result   *experiment.RunResult
	err      error
	elapsed  time.Duration
	offset   int
	width    int
	height   int
	runSlot  func(experiment.Config, uint64) (experiment.RunResult, error)
}

func New(entries []Entry, seed uint64) Model {
	// The first run is started by Init, which cannot modify the model.
	return Model{entries: entries, seed: max(1, seed), gen: 1, running: true, runSlot: experiment.RunSlot}
}

func (m Model) current() experiment.Point { return m.entries[m.selected].Points[m.point] }

// simulate starts a run of the current point; results of older runs are
// discarded by generation.
func (m *Model) simulate() tea.Cmd {
	m.gen++
	m.running = true
	return m.runCmd()
}

func (m Model) runCmd() tea.Cmd {
	gen, cfg, seed, run := m.gen, m.current().Config, m.seed, m.runSlot
	return func() tea.Msg {
		start := time.Now()
		r, err := run(cfg, seed)
		return resultMsg{gen: gen, r: r, err: err, elapsed: time.Since(start)}
	}
}

func (m Model) Init() tea.Cmd { return m.runCmd() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case resultMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.running, m.err, m.elapsed = false, msg.err, msg.elapsed
		if msg.err == nil {
			r := msg.r
			m.result = &r
		}
	case tea.KeyMsg:
		n := len(m.entries)
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up":
			m.selected, m.point, m.offset = (m.selected+n-1)%n, 0, 0
			return m, m.simulate()
		case "down":
			m.selected, m.point, m.offset = (m.selected+1)%n, 0, 0
			return m, m.simulate()
		case "left", "right":
			pts := len(m.entries[m.selected].Points)
			if msg.String() == "left" {
				m.point = (m.point + pts - 1) % pts
			} else {
				m.point = (m.point + 1) % pts
			}
			return m, m.simulate()
		case "s":
			m.seed++
			return m, m.simulate()
		case "S":
			m.seed = max(1, m.seed-1)
			return m, m.simulate()
		case "r":
			return m, m.simulate()
		case "j":
			m.offset++
		case "k":
			m.offset = max(0, m.offset-1)
		}
	}
	return m, nil
}

// Frame captures the state for rendering.
func (m Model) Frame() Frame {
	e := m.entries[m.selected]
	p := m.current()
	names := make([]string, len(m.entries))
	for i, x := range m.entries {
		names[i] = x.Name
	}
	offset := m.offset
	if m.result != nil {
		offset = min(offset, max(0, len(m.result.Lists)-1))
	}
	return Frame{
		Experiments: names, Selected: m.selected, Name: e.Name, Description: e.Config.Description,
		Point: p.Label, Points: len(e.Points), PointIndex: m.point, Seed: m.seed,
		Timeline: p.Config.Timeline, Running: m.running, Err: m.err, Result: m.result,
		Elapsed: m.elapsed, ListOffset: offset,
	}
}

func (m Model) View() string { return Render(m.Frame(), m.width, m.height) }
