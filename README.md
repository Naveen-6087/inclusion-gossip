# inclusion-gossip

A deterministic simulator for the propagation of EIP-7805 (FOCIL) inclusion lists over GossipSub. It measures:

- whether lists reach nodes before the view-freeze deadline
- what late publishers and equivocators do to the views of attesters and the next proposer
- how often the two disagree enough that attesters would reject the next block

Every experiment is a JSON file. Every run is reproducible from its seed.

```
$ go run ./cmd/ilsim run experiments/late-publisher-strict.json
  point  honest@freeze  p50 ms  p99 ms  adv@freeze  adv in attester views  adv in proposer view  ... attesters unsatisfied    max  runs >=1/3  KB/node  dups/node
     8s         100.0%     137     220      100.0%                 100.0%                100.0%  ...                  0.0%   0.0%        0.0%     1157      118.9
   8.8s         100.0%     137     220       94.1%                  94.1%                 94.0%  ...                  0.0%   0.0%        0.0%     1157      118.8
  8.85s         100.0%     137     220       66.8%                  66.8%                 76.0%  ...                  1.6%  79.0%        2.0%     1157      118.8
   8.9s         100.0%     137     220       22.5%                  22.5%                 28.0%  ...                  0.9%  29.2%        0.0%     1157      118.8
```

The full results and what they show are in [docs/results.md](docs/results.md).

## What is simulated

One slot, from 0 s to the attesters' check of the next block (13 s by default), on a network of up to a few thousand nodes.

### Network (`network/`)

- **Topology.** A random connection graph with about 50 peers per node, joined into one component.
- **Latency.** Nodes are spread over six regions using an assumed share and one-way latency for each region. Every link adds a fixed per-link spread, and every message adds jitter.
- **Bandwidth.** Each node has an upload queue: copies of a message leave one after another at the node's uplink speed (50 Mbit/s by default, with an optional share of slow nodes). Download capacity is not limited.
- **Loss.** Each message can be dropped with a configured probability.

### GossipSub (`gossip/`)

- **Mesh.** A mesh of degree D = 8 (D_low 6, D_high 12) is built from the connections.
- **Eager push.** A message is forwarded to the mesh after a validation delay.
- **Flood publishing.** Publishers send to every connected peer.
- **Seen cache.** Duplicates are suppressed by message ID.
- **Lazy repair.** Gossip uses IHAVE and IWANT on a 700 ms heartbeat with a random phase per node, with a message cache of 6 windows of which 3 are gossiped.
- **IDONTWANT** (GossipSub v1.2).
- **Validation results.** Messages can be accepted, ignored or rejected. Only accepted messages are forwarded.
- **Not modelled.** The mesh is fixed for the run: there is no GRAFT/PRUNE churn and no peer scoring. Within a single slot these matter little, but they are not modelled.

### FOCIL (`focil/`)

- **Gossip validation.** The `inclusion_list` topic is validated as in the specification:
  - size limit (MAX_BYTES_PER_INCLUSION_LIST = 8 KiB)
  - current slot, or the previous slot before the attestation deadline
  - committee root and membership
  - "first or second valid message" from each member
  - signature validity
- **Store.** Each node runs the fork-choice store's `on_inclusion_list`:
  - Lists from known equivocators are dropped.
  - A list that conflicts with a stored one marks its member as an equivocator.
  - Lists are stored only before the freeze deadline.
- **Views.** An attester's view is evaluated at the check time. It contains the stored lists whose members were not detected as equivocating by then. The proposer's view is evaluated at its 11 s cutoff.
- **Content.** The mempool holds a few hundred transactions of 110 to 600 bytes. Each committee member sees about 90% of it and fills its list with the highest-paying transactions it sees, up to 8 KiB. Honest lists therefore overlap without being identical.

### Behaviours

| Kind | What the member does |
| --- | --- |
| honest | Publishes once, uniformly between `honest.at - honest.jitter` and `honest.at` (7.5 to 8 s) |
| `late` | Publishes at `at` |
| `silent` | Never publishes |
| `invalid` | Publishes a list with a bad signature (rejected at the first hop) |
| `equivocate` | Sends version A to a `split` share of its peers at `at`, and version B to the rest at `second_at`. Version B swaps one transaction for one that no other list contains. |

### Metrics

Every run reports, for every list:

- the share of nodes that had it by the freeze deadline, by the proposer cutoff, and by the end of the run
- the delivery latency at p50, p90, p99 and maximum
- whether the proposer counts it
- the share of attesters that count it

Every run also reports:

- **List mismatch:** the share of attesters whose counted lists are not all in the proposer's view.
- **Unsatisfied attesters:** the share whose required transactions are not all among the proposer's. Those attesters would vote against a block built from the proposer's view.
- **Equivocation detection** by the freeze deadline and by the check.
- **Traffic:** bytes sent per node and duplicates received per node.
- **Coverage curves:** the mean share of nodes holding an honest list, and an adversarial one, every 100 ms from the start of the slot to the check. Silent members are left out.

Per sweep point these are summarised as mean, min, p50, p90 and max over the runs.

## Timeline and an open modelling choice

| Time | Event | Source |
| --- | --- | --- |
| 7.5 to 8 s | Committee members publish | modelling assumption |
| 9 s | Attesters freeze their inclusion-list view | view-freeze deadline, 3/4 of the slot |
| 11 s | Next proposer builds its payload | proposer inclusion-list cutoff, 1 s before the slot ends |
| 13 s | Attesters check the next block | assumed block import 1 s into the slot |
| 16 s | Last time previous-slot lists pass gossip validation | attestation deadline of the next slot |

The EIP describes the proposer listening until 11 s, so that its view is a superset of what attesters froze at 9 s. The fork-choice `on_inclusion_list` function, however, stores lists only before the freeze deadline, and the same store also serves the proposer. The simulator supports both readings:

- **`timeline.proposer_view: "cutoff"`** (the default): the proposer counts lists that arrive before its cutoff.
- **`timeline.proposer_view: "freeze"`**: the proposer's store stops at 9 s like everyone else's.

The results show the choice matters (finding 3 in [docs/results.md](docs/results.md)).

All the times are configurable.

## Usage

```bash
git clone https://github.com/Naveen-6087/inclusion-gossip
cd inclusion-gossip
go run ./cmd/ilsim check experiments/*.json
go run ./cmd/ilsim run experiments/baseline.json
go run ./cmd/ilsim run -runs 100 -seed 7 experiments/late-publisher.json
go run ./cmd/ilsim slot -point 5 -seed 3 experiments/late-publisher-strict.json
```

- **`run`** prints a table and writes `results/<name>.json` (the full effective configuration and the summaries) and `results/<name>.csv`. Add `-detail` to include every run in the JSON.
- **`slot`** simulates a single slot and lists every inclusion list, which is useful for looking at one case closely.

An experiment file looks like this:

```json
{
  "name": "late-publisher",
  "runs": 50,
  "network": {"nodes": 1000},
  "adversaries": [{"kind": "late", "at": "8.9s"}],
  "sweep": {"path": "adversaries.0.at", "values": ["8s", "8.8s", "8.9s", "9s"]}
}
```

### Configuration fields

All fields are optional except `name`. Unknown fields are rejected.

| Field | Default |
| --- | --- |
| `seed`, `runs` | 1, 20 |
| `network.nodes`, `network.peers` | 1000, 50 |
| `network.upload_mbps`, `network.slow_fraction`, `network.slow_upload_mbps` | 50, 0, 10 |
| `network.loss`, `network.link_spread`, `network.jitter_ms` | 0, 0, 0 |
| `network.regions`, `network.latency_ms` | the built-in six-region model |
| `gossip.d`, `d_low`, `d_high`, `d_lazy` | 8, 6, 12, 6 |
| `gossip.heartbeat`, `gossip.validation_delay` | "700ms", "2ms" |
| `gossip.flood_publish`, `gossip.idontwant` | true, true |
| `timeline.freeze`, `proposer_cutoff`, `attester_check` | "9s", "11s", "13s" |
| `timeline.proposer_view` | "cutoff" |
| `honest.at`, `honest.jitter` | "8s", "500ms" |
| `mempool.txs`, `min_size`, `max_size`, `visibility`, `max_il_bytes` | 300, 110, 600, 0.9, 8192 |
| `rules.lists_per_validator` | 2 (the specification's "first or second") |
| `adversaries[]` | `kind`, `count` (1), `at`, `split` (0.5), `second_at` (= `at`) |
| `sweep` | a dotted `path` into the config and a list of `values` |

## Reproducibility

- **Seeds.** Run *i* of a sweep point uses seed `seed + i`.
- **Random streams.** The topology, roles, mempool, publish times, transport jitter and gossip choices each use their own random stream derived from the seed. Changing, for example, an adversary's publish time therefore leaves the topology and the honest publish times unchanged. That is why the honest columns match across the rows of a sweep.
- **Determinism.** The event queue breaks ties by insertion order, and no map is iterated where order matters. Results are byte-identical for any number of worker goroutines; this is checked in the tests.
- **Platforms.** Floating-point arithmetic can differ between CPU architectures (fused multiply-add), so bit-for-bit equality is only expected on the same architecture. The published results were produced on amd64.

## Testing

```bash
go test ./...
go test ./... -run '^$' -bench .
```

- **`sim`.** Event ordering, including ties; independent and stable random streams.
- **`network`.** Connectivity and degree of generated graphs, determinism, configuration validation, exact upload-queue timing, and the loss rate.
- **`gossip`.**
  - Exact per-hop timing on a line.
  - Rejection stops propagation.
  - IHAVE/IWANT delivers to a node outside the mesh.
  - Gossip repairs 20% loss.
  - IDONTWANT saves duplicates and bytes.
  - Publishing modes behave as configured.
  - Runs are deterministic.
- **`focil`.**
  - Every `on_inclusion_list` rule, including late lists and conflicts after the freeze.
  - Every gossip validation rule, including the first/second/third list and the previous-slot window.
  - SSZ size and content-hash IDs.
  - IL construction (byte limit, partial overlap).
- **`experiment`.**
  - Defaults and strict parsing, validation, and sweep expansion.
  - Results are identical across worker counts and change with the seed.
  - Scenario checks: honest baseline, late lists, the strict proposer view, equivocation with and without evidence forwarding, silent and invalid members.
  - Invariants over 25 random configurations, including coverage monotonicity, quantile order, and unsatisfied ≤ mismatch.
  - Coverage curves rise monotonically, start at zero and reach every node, and a late list is absent before it is published.
- **`cmd/ilsim`.** Every shipped experiment validates, and `run` and `slot` work end to end.

A single 1,000-node slot (about 350k events) takes about 0.34 s on one core.

## Limitations

- **One slot, one inclusion-list topic.** There is no other traffic competing for bandwidth: blocks, attestations and blobs would all lengthen the upload queues.
- **Latency is an assumption.** The regional latencies and the node distribution are plausible values, not measurements.
- **Simplified block check.** The next block is assumed to contain every transaction from the proposer's view. Full blocks and invalid transactions, which the specification exempts, are not modelled.
- **Abstract signatures.** Signatures are modelled by size and a validity flag, and BLS verification by a fixed delay.
