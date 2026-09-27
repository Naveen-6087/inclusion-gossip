package experiment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Naveen-6087/inclusion-gossip/focil"
	"github.com/Naveen-6087/inclusion-gossip/gossip"
	"github.com/Naveen-6087/inclusion-gossip/network"
)

// Duration is a time.Duration written as a string such as "8.5s" in JSON.
type Duration struct{ time.Duration }

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"8.5s\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func dur(d time.Duration) Duration { return Duration{d} }

type Config struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Seed        uint64              `json:"seed"`
	Runs        int                 `json:"runs"`
	Network     network.Config      `json:"network"`
	Gossip      GossipConfig        `json:"gossip"`
	Timeline    Timeline            `json:"timeline"`
	Mempool     focil.MempoolConfig `json:"mempool"`
	Rules       Rules               `json:"rules"`
	Honest      Honest              `json:"honest"`
	Adversaries []Adversary         `json:"adversaries,omitempty"`
	Sweep       *Sweep              `json:"sweep,omitempty"`
}

type GossipConfig struct {
	D               int      `json:"d,omitempty"`
	DLow            int      `json:"d_low,omitempty"`
	DHigh           int      `json:"d_high,omitempty"`
	DLazy           int      `json:"d_lazy,omitempty"`
	Heartbeat       Duration `json:"heartbeat,omitzero"`
	FloodPublish    *bool    `json:"flood_publish,omitempty"`
	IDontWant       *bool    `json:"idontwant,omitempty"`
	ValidationDelay Duration `json:"validation_delay,omitzero"`
}

func (g GossipConfig) Params() gossip.Params {
	p := gossip.DefaultParams()
	if g.D > 0 {
		p.D = g.D
	}
	if g.DLow > 0 {
		p.DLow = g.DLow
	}
	if g.DHigh > 0 {
		p.DHigh = g.DHigh
	}
	if g.DLazy > 0 {
		p.DLazy = g.DLazy
	}
	if g.Heartbeat.Duration > 0 {
		p.Heartbeat = g.Heartbeat.Duration
	}
	if g.FloodPublish != nil {
		p.FloodPublish = *g.FloodPublish
	}
	if g.IDontWant != nil {
		p.IDontWant = *g.IDontWant
	}
	if g.ValidationDelay.Duration > 0 {
		p.ValidationDelay = g.ValidationDelay.Duration
	}
	return p
}

// Timeline holds the deadlines, measured from the start of the slot whose
// inclusion lists are studied.
type Timeline struct {
	Freeze         Duration `json:"freeze,omitzero"`
	ProposerCutoff Duration `json:"proposer_cutoff,omitzero"`
	// "cutoff": the next proposer counts lists that arrive before its
	// cutoff. "freeze": its store stops at the view-freeze deadline like
	// everyone else's, as on_inclusion_list is written.
	ProposerView string `json:"proposer_view,omitempty"`
	// When attesters check the next slot's block against their view.
	AttesterCheck Duration `json:"attester_check,omitzero"`
}

// Rules adjusts the inclusion_list gossip validation.
type Rules struct {
	// Distinct lists per committee member accepted and forwarded; the
	// specification's "first or second valid message" is 2.
	ListsPerValidator int `json:"lists_per_validator,omitempty"`
}

// Honest members publish uniformly within [At-Jitter, At].
type Honest struct {
	At     Duration `json:"at,omitzero"`
	Jitter Duration `json:"jitter,omitzero"`
}

// Adversary kinds.
const (
	KindLate       = "late"       // publishes at At
	KindSilent     = "silent"     // never publishes
	KindInvalid    = "invalid"    // publishes a list with a bad signature at At
	KindEquivocate = "equivocate" // two versions to disjoint sets of peers
)

type Adversary struct {
	Kind  string   `json:"kind"`
	Count *int     `json:"count,omitempty"` // committee members with this behaviour, default 1
	At    Duration `json:"at,omitzero"`
	// Equivocation: version A goes to a Split share of the member's peers at
	// At, version B (one transaction swapped for one nobody else lists) to
	// the rest at SecondAt.
	Split    float64  `json:"split,omitempty"`
	SecondAt Duration `json:"second_at,omitzero"`
}

func (a Adversary) N() int {
	if a.Count == nil {
		return 1
	}
	return *a.Count
}

// Sweep varies one setting. Path addresses a field of the JSON config with
// dots, e.g. "adversaries.0.at" or "network.loss".
type Sweep struct {
	Path   string            `json:"path"`
	Values []json.RawMessage `json:"values"`
}

func (c Config) WithDefaults() Config {
	if c.Runs == 0 {
		c.Runs = 20
	}
	if c.Seed == 0 {
		c.Seed = 1
	}
	if c.Network.Nodes == 0 {
		c.Network.Nodes = 1000
	}
	c.Network = c.Network.WithDefaults()
	if c.Timeline.Freeze.Duration == 0 {
		c.Timeline.Freeze = dur(focil.ViewFreezeDeadline)
	}
	if c.Timeline.ProposerCutoff.Duration == 0 {
		c.Timeline.ProposerCutoff = dur(focil.ProposerCutoff)
	}
	if c.Timeline.ProposerView == "" {
		c.Timeline.ProposerView = "cutoff"
	}
	if c.Timeline.AttesterCheck.Duration == 0 {
		c.Timeline.AttesterCheck = dur(focil.SecondsPerSlot + time.Second)
	}
	if c.Honest.At.Duration == 0 {
		c.Honest.At = dur(8 * time.Second)
	}
	if c.Honest.Jitter.Duration == 0 {
		c.Honest.Jitter = dur(500 * time.Millisecond)
	}
	c.Mempool = c.Mempool.WithDefaults()
	if c.Rules.ListsPerValidator == 0 {
		c.Rules.ListsPerValidator = 2
	}
	for i := range c.Adversaries {
		a := &c.Adversaries[i]
		if a.Kind == KindEquivocate {
			if a.Split == 0 {
				a.Split = 0.5
			}
			if a.SecondAt.Duration == 0 {
				a.SecondAt = a.At
			}
		}
	}
	return c
}

func (c Config) Validate() error {
	if c.Name == "" {
		return errors.New("experiment: name is required")
	}
	if c.Runs < 1 {
		return errors.New("experiment: runs must be positive")
	}
	if err := c.Network.Validate(); err != nil {
		return err
	}
	if c.Network.Nodes <= focil.CommitteeSize {
		return fmt.Errorf("experiment: need more than %d nodes", focil.CommitteeSize)
	}
	t := c.Timeline
	if !(t.Freeze.Duration <= t.ProposerCutoff.Duration && t.ProposerCutoff.Duration <= t.AttesterCheck.Duration) {
		return errors.New("experiment: timeline must satisfy freeze <= proposer_cutoff <= attester_check")
	}
	if t.ProposerView != "cutoff" && t.ProposerView != "freeze" {
		return fmt.Errorf("experiment: proposer_view must be \"cutoff\" or \"freeze\", got %q", t.ProposerView)
	}
	if c.Rules.ListsPerValidator < 1 {
		return errors.New("experiment: rules.lists_per_validator must be at least 1")
	}
	if c.Honest.Jitter.Duration > c.Honest.At.Duration {
		return errors.New("experiment: honest jitter larger than publish time")
	}
	if c.Mempool.MaxBytes > focil.MaxBytesPerInclusionList || c.Mempool.MinSize > c.Mempool.MaxSize || c.Mempool.Visibility > 1 {
		return errors.New("experiment: invalid mempool settings")
	}
	n := 0
	for _, a := range c.Adversaries {
		switch a.Kind {
		case KindLate, KindSilent, KindInvalid, KindEquivocate:
		default:
			return fmt.Errorf("experiment: unknown adversary kind %q", a.Kind)
		}
		if a.N() < 0 || a.Split < 0 || a.Split > 1 {
			return fmt.Errorf("experiment: invalid %s adversary", a.Kind)
		}
		if a.Kind == KindEquivocate && a.SecondAt.Duration < a.At.Duration {
			return errors.New("experiment: second_at before at")
		}
		n += a.N()
	}
	if n > focil.CommitteeSize {
		return fmt.Errorf("experiment: %d adversaries in a committee of %d", n, focil.CommitteeSize)
	}
	return nil
}

// Load reads a JSON experiment file. Unknown fields are an error, so a
// typo cannot silently fall back to a default.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

func Parse(data []byte) (Config, error) {
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("experiment: %w", err)
	}
	return c, nil
}

// Point is one configuration of a sweep.
type Point struct {
	Label  string
	Config Config
}

// Expand returns the sweep points, or the configuration itself if there is
// no sweep. Each point is validated.
func (c Config) Expand() ([]Point, error) {
	if c.Sweep == nil {
		cfg := c.WithDefaults()
		return []Point{{Label: "-", Config: cfg}}, cfg.Validate()
	}
	if len(c.Sweep.Values) == 0 {
		return nil, errors.New("experiment: sweep has no values")
	}
	base := c
	base.Sweep = nil
	raw, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	var points []Point
	for _, v := range c.Sweep.Values {
		var tree any
		if err := json.Unmarshal(raw, &tree); err != nil {
			return nil, err
		}
		var val any
		if err := json.Unmarshal(v, &val); err != nil {
			return nil, err
		}
		if tree, err = setPath(tree, strings.Split(c.Sweep.Path, "."), val); err != nil {
			return nil, fmt.Errorf("experiment: sweep path %q: %w", c.Sweep.Path, err)
		}
		b, _ := json.Marshal(tree)
		pc, err := Parse(b)
		if err != nil {
			return nil, err
		}
		pc = pc.WithDefaults()
		if err := pc.Validate(); err != nil {
			return nil, fmt.Errorf("sweep value %s: %w", v, err)
		}
		label := strings.Trim(string(v), `"`)
		points = append(points, Point{Label: label, Config: pc})
	}
	return points, nil
}

func setPath(node any, path []string, val any) (any, error) {
	if len(path) == 0 {
		return val, nil
	}
	switch n := node.(type) {
	case map[string]any:
		child, err := setPath(n[path[0]], path[1:], val)
		if err != nil {
			return nil, err
		}
		n[path[0]] = child
		return n, nil
	case []any:
		i, err := strconv.Atoi(path[0])
		if err != nil || i < 0 || i >= len(n) {
			return nil, fmt.Errorf("index %q out of range", path[0])
		}
		child, err := setPath(n[i], path[1:], val)
		if err != nil {
			return nil, err
		}
		n[i] = child
		return n, nil
	case nil:
		m := map[string]any{}
		return setPath(m, path, val)
	default:
		return nil, fmt.Errorf("cannot descend into %T at %q", node, path[0])
	}
}
