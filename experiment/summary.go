package experiment

import (
	"encoding/csv"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"text/tabwriter"
)

// Stat summarises one metric over the runs of a point.
type Stat struct {
	Mean float64 `json:"mean"`
	Min  float64 `json:"min"`
	P50  float64 `json:"p50"`
	P90  float64 `json:"p90"`
	Max  float64 `json:"max"`
}

func stat(xs []float64) Stat {
	if len(xs) == 0 {
		return Stat{}
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	sum := 0.0
	for _, x := range s {
		sum += x
	}
	return Stat{Mean: sum / float64(len(s)), Min: s[0], P50: quantile(s, 0.5), P90: quantile(s, 0.9), Max: s[len(s)-1]}
}

// Summary aggregates the runs of one sweep point.
type Summary struct {
	Label string `json:"label"`
	Runs  int    `json:"runs"`

	HonestAtFreeze Stat `json:"honest_at_freeze"` // per list
	HonestP50Ms    Stat `json:"honest_p50_ms"`
	HonestP99Ms    Stat `json:"honest_p99_ms"`
	HonestMaxMs    Stat `json:"honest_max_ms"`

	AdversaryAtFreeze  *Stat `json:"adversary_at_freeze,omitempty"`
	AdversaryAtCutoff  *Stat `json:"adversary_at_cutoff,omitempty"`
	AdversaryInViews   *Stat `json:"adversary_attester_view,omitempty"` // share of attesters counting it
	AdversaryProposer  *Stat `json:"adversary_in_proposer_view,omitempty"`
	EquivDetectFreeze  *Stat `json:"equivocation_detected_at_freeze,omitempty"`
	EquivDetectCheck   *Stat `json:"equivocation_detected_at_check,omitempty"`
	EquivProposerFound *Stat `json:"equivocation_proposer_detected,omitempty"`

	ListMismatch  Stat `json:"list_mismatch"`
	TxUnsatisfied Stat `json:"tx_unsatisfied"`
	// Share of runs in which at least a third of attesters would reject the
	// block for not satisfying the inclusion lists.
	RunsThirdUnsatisfied float64 `json:"runs_third_unsatisfied"`
	ProposerLists        Stat    `json:"proposer_lists"`
	KBSentPerNode        Stat    `json:"kb_sent_per_node"`
	DuplicatesPerNode    Stat    `json:"duplicates_per_node"`
	Events               Stat    `json:"events"`
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func Summarize(label string, runs []RunResult) Summary {
	s := Summary{Label: label, Runs: len(runs)}
	var hFreeze, hP50, hP99, hMax []float64
	var aFreeze, aCutoff, aViews, aProp []float64
	var eFreeze, eCheck, eProp []float64
	var mism, unsat, plists, kb, dups, events []float64
	third := 0
	for _, r := range runs {
		for _, l := range r.Lists {
			switch l.Kind {
			case "honest":
				hFreeze = append(hFreeze, l.AtFreeze)
				hP50 = append(hP50, l.P50Ms)
				hP99 = append(hP99, l.P99Ms)
				hMax = append(hMax, l.MaxMs)
			case KindSilent:
			default:
				aFreeze = append(aFreeze, l.AtFreeze)
				aCutoff = append(aCutoff, l.AtCutoff)
				aViews = append(aViews, l.AttesterView)
				aProp = append(aProp, b2f(l.InProposer))
			}
		}
		for _, e := range r.Equivocations {
			eFreeze = append(eFreeze, e.DetectedAtFreeze)
			eCheck = append(eCheck, e.DetectedAtCheck)
			eProp = append(eProp, b2f(e.ProposerDetected))
		}
		mism = append(mism, r.ListMismatch)
		unsat = append(unsat, r.TxUnsatisfied)
		if r.TxUnsatisfied >= 1.0/3 {
			third++
		}
		plists = append(plists, float64(r.ProposerLists))
		kb = append(kb, r.MeanBytesSent/1000)
		dups = append(dups, r.MeanDups)
		events = append(events, float64(r.Events))
	}
	s.HonestAtFreeze, s.HonestP50Ms, s.HonestP99Ms, s.HonestMaxMs = stat(hFreeze), stat(hP50), stat(hP99), stat(hMax)
	opt := func(xs []float64) *Stat {
		if len(xs) == 0 {
			return nil
		}
		st := stat(xs)
		return &st
	}
	s.AdversaryAtFreeze, s.AdversaryAtCutoff = opt(aFreeze), opt(aCutoff)
	s.AdversaryInViews, s.AdversaryProposer = opt(aViews), opt(aProp)
	s.EquivDetectFreeze, s.EquivDetectCheck, s.EquivProposerFound = opt(eFreeze), opt(eCheck), opt(eProp)
	s.ListMismatch, s.TxUnsatisfied = stat(mism), stat(unsat)
	if len(runs) > 0 {
		s.RunsThirdUnsatisfied = float64(third) / float64(len(runs))
	}
	s.ProposerLists, s.KBSentPerNode, s.DuplicatesPerNode, s.Events = stat(plists), stat(kb), stat(dups), stat(events)
	return s
}

// Result is everything an experiment produces.
type Result struct {
	Config    Config        `json:"config"`
	Points    []Point       `json:"-"`
	Summaries []Summary     `json:"summaries"`
	Runs      [][]RunResult `json:"runs,omitempty"`
}

// Run executes every sweep point with seeds Seed, Seed+1, ... using up to
// workers goroutines. Results do not depend on the number of workers.
func Run(cfg Config, workers int, keepRuns bool) (Result, error) {
	points, err := cfg.Expand()
	if err != nil {
		return Result{}, err
	}
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	type job struct{ p, i int }
	runs := make([][]RunResult, len(points))
	for p := range points {
		runs[p] = make([]RunResult, points[p].Config.Runs)
	}
	jobs := make(chan job)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				pc := points[j.p].Config
				r, err := RunSlot(pc, pc.Seed+uint64(j.i))
				if err != nil {
					errs <- err
					continue
				}
				runs[j.p][j.i] = r
			}
		}()
	}
	for p := range points {
		for i := range points[p].Config.Runs {
			jobs <- job{p, i}
		}
	}
	close(jobs)
	wg.Wait()
	close(errs)
	if err := <-errs; err != nil {
		return Result{}, err
	}
	res := Result{Config: cfg, Points: points}
	for p, pt := range points {
		res.Summaries = append(res.Summaries, Summarize(pt.Label, runs[p]))
	}
	if keepRuns {
		res.Runs = runs
	}
	return res, nil
}

func pct(x float64) string { return strconv.FormatFloat(100*x, 'f', 1, 64) + "%" }
func msf(x float64) string { return strconv.FormatFloat(x, 'f', 0, 64) }

func optPct(s *Stat) string {
	if s == nil {
		return "-"
	}
	return pct(s.Mean)
}

// PrintTable writes one row per sweep point.
func (r Result) PrintTable(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "point\thonest@freeze\tp50 ms\tp99 ms\tadv@freeze\tadv in attester views\tadv in proposer view\tequiv detected@check\tattesters unsatisfied\tmax\truns >=1/3\tKB/node\tdups/node\t")
	for _, s := range r.Summaries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%.0f\t%.1f\t\n",
			s.Label, pct(s.HonestAtFreeze.Mean), msf(s.HonestP50Ms.Mean), msf(s.HonestP99Ms.Mean),
			optPct(s.AdversaryAtFreeze), optPct(s.AdversaryInViews), optPct(s.AdversaryProposer), optPct(s.EquivDetectCheck),
			pct(s.TxUnsatisfied.Mean), pct(s.TxUnsatisfied.Max), pct(s.RunsThirdUnsatisfied),
			s.KBSentPerNode.Mean, s.DuplicatesPerNode.Mean)
	}
	return tw.Flush()
}

// WriteCSV writes the summaries with one row per sweep point.
func (r Result) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	header := []string{"point", "runs", "honest_at_freeze", "honest_at_freeze_min", "honest_p50_ms", "honest_p99_ms", "honest_max_ms",
		"adversary_at_freeze", "adversary_at_cutoff", "adversary_attester_view", "adversary_in_proposer_view",
		"equivocation_detected_at_freeze", "equivocation_detected_at_check",
		"list_mismatch", "tx_unsatisfied", "tx_unsatisfied_max", "runs_third_unsatisfied",
		"proposer_lists", "kb_sent_per_node", "duplicates_per_node", "events"}
	if err := cw.Write(header); err != nil {
		return err
	}
	f := func(x float64) string { return strconv.FormatFloat(x, 'g', 6, 64) }
	o := func(s *Stat) string {
		if s == nil {
			return ""
		}
		return f(s.Mean)
	}
	for _, s := range r.Summaries {
		row := []string{s.Label, strconv.Itoa(s.Runs), f(s.HonestAtFreeze.Mean), f(s.HonestAtFreeze.Min), f(s.HonestP50Ms.Mean), f(s.HonestP99Ms.Mean), f(s.HonestMaxMs.Mean),
			o(s.AdversaryAtFreeze), o(s.AdversaryAtCutoff), o(s.AdversaryInViews), o(s.AdversaryProposer),
			o(s.EquivDetectFreeze), o(s.EquivDetectCheck),
			f(s.ListMismatch.Mean), f(s.TxUnsatisfied.Mean), f(s.TxUnsatisfied.Max), f(s.RunsThirdUnsatisfied),
			f(s.ProposerLists.Mean), f(s.KBSentPerNode.Mean), f(s.DuplicatesPerNode.Mean), f(s.Events.Mean)}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
