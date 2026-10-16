package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Naveen-6087/inclusion-gossip/experiment"
)

// Frame is everything one screen shows.
type Frame struct {
	Experiments []string
	Selected    int
	Name        string
	Description string
	Point       string
	Points      int
	PointIndex  int
	Seed        uint64
	Timeline    experiment.Timeline
	Running     bool
	Err         error
	Result      *experiment.RunResult
	Elapsed     time.Duration
	ListOffset  int
}

var (
	accent   = lipgloss.Color("#5FAFFF")
	good     = lipgloss.Color("#87D787")
	warn     = lipgloss.Color("#FFAF5F")
	bad      = lipgloss.Color("#FF5F5F")
	muted    = lipgloss.Color("#8A8A8A")
	titleSty = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#005F87")).Padding(0, 1)
	boxSty   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#3A3A3A")).Padding(0, 1)
	headSty  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	labelSty = lipgloss.NewStyle().Foreground(muted)
)

const sparkChars = "▁▂▃▄▅▆▇█"

// Sparkline draws vals, each in [0, 1], one character per value.
func Sparkline(vals []float64) string {
	runes := []rune(sparkChars)
	var b strings.Builder
	for _, v := range vals {
		if v <= 0 {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(runes[min(len(runes)-1, int(v*float64(len(runes)-1)+0.5))])
	}
	return b.String()
}

// Bar is a horizontal bar of width w filled to frac.
func Bar(frac float64, w int) string {
	n := max(0, min(w, int(frac*float64(w)+0.5)))
	return strings.Repeat("█", n) + strings.Repeat("░", w-n)
}

// Resample picks width values evenly from vals.
func Resample(vals []float64, width int) []float64 {
	if width <= 0 || len(vals) == 0 {
		return nil
	}
	out := make([]float64, width)
	for i := range out {
		out[i] = vals[min(len(vals)-1, i*len(vals)/width)]
	}
	return out
}

func box(title string, body []string, width int) string {
	return boxSty.Width(width).Render(headSty.Render(title) + "\n" + strings.Join(body, "\n"))
}

func pct(x float64) string { return fmt.Sprintf("%5.1f%%", 100*x) }

func kindColor(kind string) lipgloss.Color {
	switch kind {
	case "honest":
		return good
	case experiment.KindLate:
		return warn
	default:
		return bad
	}
}

// Render draws the frame for a terminal of the given size.
func Render(f Frame, width, height int) string {
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 40
	}
	status := lipgloss.NewStyle().Foreground(good).Render("● done")
	switch {
	case f.Running:
		status = lipgloss.NewStyle().Foreground(warn).Render("◌ simulating…")
	case f.Err != nil:
		status = lipgloss.NewStyle().Foreground(bad).Render("✗ " + f.Err.Error())
	case f.Elapsed > 0:
		status += labelSty.Render(fmt.Sprintf(" in %v", f.Elapsed.Round(time.Millisecond)))
	}
	header := lipgloss.JoinHorizontal(lipgloss.Center,
		titleSty.Render("ilsim"), "  ", lipgloss.NewStyle().Bold(true).Render(f.Name),
		labelSty.Render(fmt.Sprintf("   point %s (%d/%d)   seed %d   ", f.Point, f.PointIndex+1, f.Points, f.Seed)), status)

	sideW := 26
	for _, e := range f.Experiments {
		sideW = max(sideW, len(e)+6)
	}
	var side []string
	for i, e := range f.Experiments {
		if i == f.Selected {
			side = append(side, lipgloss.NewStyle().Bold(true).Foreground(accent).Render("▸ "+e))
		} else {
			side = append(side, "  "+e)
		}
	}
	sidebar := box("Experiments", side, sideW)
	mainW := max(60, width-sideW-4)

	var body string
	if r := f.Result; r != nil {
		body = lipgloss.JoinVertical(lipgloss.Left,
			coverageBox(f, r, mainW),
			lipgloss.JoinHorizontal(lipgloss.Top, outcomeBox(r, mainW/2-1), trafficBox(r, mainW-mainW/2-1)),
			listsBox(f, r, mainW, max(4, height-30)))
	} else {
		body = box("Slot", []string{labelSty.Render("waiting for the first run…")}, mainW)
	}
	footer := labelSty.Render("↑↓ experiment   ←→ sweep point   s/S seed   j/k scroll lists   r rerun   q quit")
	return lipgloss.JoinVertical(lipgloss.Left, header, "",
		lipgloss.JoinHorizontal(lipgloss.Top, sidebar, body), footer)
}

func coverageBox(f Frame, r *experiment.RunResult, width int) string {
	inner := width - 14
	// Lists are published around 7.5 to 9 s: show the window from 7 s on.
	from := 0
	if r.CurveStepMs > 0 {
		from = min(len(r.HonestCurve)-1, int(7000/r.CurveStepMs))
	}
	marker := func(at time.Duration) int {
		if r.CurveStepMs <= 0 || len(r.HonestCurve) == from+1 {
			return -1
		}
		i := int(float64(at.Milliseconds())/r.CurveStepMs) - from
		return i * inner / (len(r.HonestCurve) - from)
	}
	axis := []rune(strings.Repeat("─", inner))
	freeze, cutoff := marker(f.Timeline.Freeze.Duration), marker(f.Timeline.ProposerCutoff.Duration)
	if freeze >= 0 && freeze < inner {
		axis[freeze] = '┃'
	}
	if cutoff >= 0 && cutoff < inner {
		axis[cutoff] = '┃'
	}
	lines := []string{
		labelSty.Render("honest     ") + lipgloss.NewStyle().Foreground(good).Render(Sparkline(Resample(r.HonestCurve[from:], inner))),
	}
	if len(r.AdversaryCurve) > 0 {
		lines = append(lines, labelSty.Render("adversary  ")+lipgloss.NewStyle().Foreground(bad).Render(Sparkline(Resample(r.AdversaryCurve[from:], inner))))
	}
	lines = append(lines,
		labelSty.Render("           "+string(axis)),
		labelSty.Render(fmt.Sprintf("           7s … %v   ┃ freeze %v   ┃ proposer cutoff %v",
			f.Timeline.AttesterCheck.Duration, f.Timeline.Freeze.Duration, f.Timeline.ProposerCutoff.Duration)))
	return box("Share of nodes holding a list", lines, width)
}

func outcomeBox(r *experiment.RunResult, width int) string {
	unsat := lipgloss.NewStyle().Foreground(good)
	if r.TxUnsatisfied >= 1.0/3 {
		unsat = lipgloss.NewStyle().Foreground(bad).Bold(true)
	} else if r.TxUnsatisfied > 0 {
		unsat = lipgloss.NewStyle().Foreground(warn)
	}
	lines := []string{
		fmt.Sprintf("%s %d", labelSty.Render("proposer counts lists   "), r.ProposerLists),
		fmt.Sprintf("%s %.2f", labelSty.Render("attesters count (mean)  "), r.MeanLists),
		fmt.Sprintf("%s %s", labelSty.Render("attesters list mismatch "), pct(r.ListMismatch)),
		fmt.Sprintf("%s %s", labelSty.Render("would reject the block  "), unsat.Render(pct(r.TxUnsatisfied))),
	}
	for _, e := range r.Equivocations {
		lines = append(lines, lipgloss.NewStyle().Foreground(bad).Render(fmt.Sprintf(
			"equivocator %d: seen by %s at freeze, %s at check, proposer %v",
			e.Validator, strings.TrimSpace(pct(e.DetectedAtFreeze)), strings.TrimSpace(pct(e.DetectedAtCheck)), e.ProposerDetected)))
	}
	return box("Next block", lines, width)
}

func trafficBox(r *experiment.RunResult, width int) string {
	return box("Traffic", []string{
		fmt.Sprintf("%s %.0f KB mean, %.0f KB max", labelSty.Render("sent per node "), r.MeanBytesSent/1000, float64(r.MaxBytesSent)/1000),
		fmt.Sprintf("%s %.1f", labelSty.Render("dups per node "), r.MeanDups),
		fmt.Sprintf("%s %d", labelSty.Render("dropped       "), r.Dropped),
		fmt.Sprintf("%s %d (proposer node %d)", labelSty.Render("events        "), r.Events, r.Proposer),
	}, width)
}

func listsBox(f Frame, r *experiment.RunResult, width, rows int) string {
	barW := max(8, min(20, (width-70)/2))
	lines := []string{labelSty.Render(fmt.Sprintf("%7s  %-10s %7s  %-*s %-*s %6s  %s",
		"member", "kind", "pub ms", barW+7, "@freeze", barW+7, "attesters", "p99 ms", "proposer"))}
	start := max(0, min(f.ListOffset, len(r.Lists)-rows))
	end := min(len(r.Lists), start+rows)
	for _, l := range r.Lists[start:end] {
		pub := "-"
		if l.PublishedMs >= 0 {
			pub = fmt.Sprintf("%.0f", l.PublishedMs)
		}
		mark := lipgloss.NewStyle().Foreground(bad).Render("✗")
		if l.InProposer {
			mark = lipgloss.NewStyle().Foreground(good).Render("✓")
		}
		color := lipgloss.NewStyle().Foreground(kindColor(l.Kind))
		lines = append(lines, fmt.Sprintf("%7d  %s %7s  %s %s %s %s %6.0f  %s",
			l.Validator, color.Render(fmt.Sprintf("%-10s", l.Kind)), pub,
			color.Render(Bar(l.AtFreeze, barW)), pct(l.AtFreeze),
			lipgloss.NewStyle().Foreground(accent).Render(Bar(l.AttesterView, barW)), pct(l.AttesterView),
			l.P99Ms, mark))
	}
	title := fmt.Sprintf("Inclusion lists (%d–%d of %d)", start+1, end, len(r.Lists))
	return box(title, lines, width)
}
