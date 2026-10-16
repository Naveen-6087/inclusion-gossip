package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Naveen-6087/inclusion-gossip/experiment"
	"github.com/Naveen-6087/inclusion-gossip/tui"
)

const usage = `usage: ilsim <command> [flags] <experiment.json>...

commands:
  run       run experiments and write results
  slot      simulate a single slot and list every inclusion list
  check     validate experiment files without running them
  tui       explore single slots interactively (default: experiments/*.json)

run "ilsim <command> -h" for flags`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "ilsim:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:], out)
	case "slot":
		return cmdSlot(args[1:], out)
	case "check":
		return cmdCheck(args[1:], out)
	case "tui":
		return cmdTUI(args[1:])
	case "-h", "--help", "help":
		fmt.Fprintln(out, usage)
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", args[0], usage)
}

func cmdRun(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	outDir := fs.String("out", "results", "directory for <name>.json and <name>.csv (empty: do not write)")
	runs := fs.Int("runs", 0, "override the number of runs per point")
	seed := fs.Uint64("seed", 0, "override the first seed")
	workers := fs.Int("workers", 0, "parallel runs (default: all CPUs); results do not depend on it")
	detail := fs.Bool("detail", false, "include every run in the JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("run: no experiment files")
	}
	for _, path := range fs.Args() {
		cfg, err := experiment.Load(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if *runs > 0 {
			cfg.Runs = *runs
		}
		if *seed > 0 {
			cfg.Seed = *seed
		}
		start := time.Now()
		res, err := experiment.Run(cfg, *workers, *detail)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		fmt.Fprintf(out, "%s: %s\n", cfg.Name, cfg.Description)
		if err := res.PrintTable(out); err != nil {
			return err
		}
		fmt.Fprintf(out, "(%d points x %d runs in %v)\n\n", len(res.Summaries), res.Summaries[0].Runs, time.Since(start).Round(time.Millisecond))
		if *outDir != "" {
			if err := write(*outDir, cfg.Name, res); err != nil {
				return err
			}
		}
	}
	return nil
}

func write(dir, name string, res experiment.Result) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, name+".csv"))
	if err != nil {
		return err
	}
	if err := res.WriteCSV(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func cmdSlot(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("slot", flag.ContinueOnError)
	seed := fs.Uint64("seed", 1, "seed")
	point := fs.Int("point", 0, "sweep point index")
	asJSON := fs.Bool("json", false, "print the full result as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("slot: want exactly one experiment file")
	}
	cfg, err := experiment.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	points, err := cfg.Expand()
	if err != nil {
		return err
	}
	if *point < 0 || *point >= len(points) {
		return fmt.Errorf("slot: point %d out of range (0-%d)", *point, len(points)-1)
	}
	r, err := experiment.RunSlot(points[*point].Config, *seed)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Experiment string               `json:"experiment"`
			Point      string               `json:"point"`
			Result     experiment.RunResult `json:"result"`
		}{cfg.Name, points[*point].Label, r})
	}
	fmt.Fprintf(out, "%s, point %s, seed %d: %d events, proposer node %d\n\n", cfg.Name, points[*point].Label, *seed, r.Events, r.Proposer)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "member\tkind\tpublished ms\t@freeze\t@cutoff\tfinal\tp50 ms\tp99 ms\tmax ms\tproposer\tattesters\t")
	for _, l := range r.Lists {
		fmt.Fprintf(tw, "%d\t%s\t%.0f\t%.1f%%\t%.1f%%\t%.1f%%\t%.0f\t%.0f\t%.0f\t%v\t%.1f%%\t\n",
			l.Validator, l.Kind, l.PublishedMs, 100*l.AtFreeze, 100*l.AtCutoff, 100*l.Final, l.P50Ms, l.P99Ms, l.MaxMs, l.InProposer, 100*l.AttesterView)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, e := range r.Equivocations {
		fmt.Fprintf(out, "\nequivocator %d: detected by %.1f%% of attesters at the freeze deadline, %.1f%% at the check; proposer detected: %v\n",
			e.Validator, 100*e.DetectedAtFreeze, 100*e.DetectedAtCheck, e.ProposerDetected)
	}
	fmt.Fprintf(out, "\nproposer counts %d lists; attesters count %.2f on average\n", r.ProposerLists, r.MeanLists)
	fmt.Fprintf(out, "attesters whose lists the proposer lacks: %.2f%%; that would reject the block: %.2f%%\n", 100*r.ListMismatch, 100*r.TxUnsatisfied)
	fmt.Fprintf(out, "sent per node: %.0f KB mean, %.0f KB max; duplicates per node: %.1f; dropped: %d\n",
		r.MeanBytesSent/1000, float64(r.MaxBytesSent)/1000, r.MeanDups, r.Dropped)
	return nil
}

func cmdCheck(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("check: no experiment files")
	}
	for _, path := range args {
		cfg, err := experiment.Load(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		points, err := cfg.Expand()
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		fmt.Fprintf(out, "%s: ok (%s, %d points x %d runs)\n", path, cfg.Name, len(points), points[0].Config.Runs)
	}
	return nil
}

func cmdTUI(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	seed := fs.Uint64("seed", 1, "first seed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	paths := fs.Args()
	if len(paths) == 0 {
		paths, _ = filepath.Glob(filepath.Join("experiments", "*.json"))
		if len(paths) == 0 {
			return errors.New("tui: no experiment files given and none in ./experiments")
		}
	}
	entries, err := tui.LoadEntries(paths)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(tui.New(entries, *seed), tea.WithAltScreen()).Run()
	return err
}
