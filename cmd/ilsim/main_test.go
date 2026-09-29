package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckShippedExperiments(t *testing.T) {
	files, _ := filepath.Glob("../../experiments/*.json")
	if len(files) < 5 {
		t.Fatalf("found %d experiment files", len(files))
	}
	var out bytes.Buffer
	if err := run(append([]string{"check"}, files...), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), ": ok") != len(files) {
		t.Fatalf("check output:\n%s", out.String())
	}
}

func TestRunWritesResults(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "exp.json")
	os.WriteFile(cfg, []byte(`{"name": "tiny", "network": {"nodes": 60, "peers": 10},
		"adversaries": [{"kind": "late", "at": "8.9s"}],
		"sweep": {"path": "adversaries.0.at", "values": ["8s", "9.5s"]}}`), 0o644)
	var out bytes.Buffer
	if err := run([]string{"run", "-runs", "3", "-out", dir, "-detail", cfg}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "tiny") || !strings.Contains(out.String(), "9.5s") {
		t.Fatalf("output:\n%s", out.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "tiny.json"))
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Summaries []struct{ Runs int }
		Runs      [][]json.RawMessage
	}
	if err := json.Unmarshal(data, &res); err != nil || len(res.Summaries) != 2 || res.Summaries[0].Runs != 3 || len(res.Runs) != 2 {
		t.Fatalf("results file: %v %+v", err, res)
	}
	csv, _ := os.ReadFile(filepath.Join(dir, "tiny.csv"))
	if strings.Count(string(csv), "\n") != 3 {
		t.Fatalf("csv:\n%s", csv)
	}
}

func TestSlot(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "exp.json")
	os.WriteFile(cfg, []byte(`{"name": "one", "network": {"nodes": 80, "peers": 10},
		"adversaries": [{"kind": "equivocate", "at": "8s"}]}`), 0o644)
	var out bytes.Buffer
	if err := run([]string{"slot", "-seed", "3", cfg}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"equivocate", "honest", "equivocator", "proposer counts"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output lacks %q:\n%s", want, out.String())
		}
	}
	if err := run([]string{"slot", "-point", "4", cfg}, &out); err == nil {
		t.Fatal("bad point accepted")
	}
}

func TestErrors(t *testing.T) {
	var out bytes.Buffer
	for _, args := range [][]string{nil, {"bogus"}, {"run"}, {"check", "missing.json"}} {
		if err := run(args, &out); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
