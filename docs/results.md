# Results

Output of every shipped experiment, produced with:

```bash
go run ./cmd/ilsim run experiments/*.json
```

All numbers come from the simulator and its model (see the README), not from a real network. Each row averages the stated number of runs. Every run uses its own seed, which gives it its own topology, roles, mempool and publish times. The same seeds give byte-identical results; this was checked on amd64.

Unless an experiment says otherwise, the setup is:

- 1,000 nodes, each with about 50 connections, a 50 Mbit/s uplink and a GossipSub mesh of degree 8.
- An IL committee of 16 members, publishing honestly between 7.5 s and 8 s.
- Lists of about 8 KB each, attester view freeze at 9 s, and a proposer cutoff at 11 s.
- Attesters check the next block 1 s into the next slot (13 s).

Some terms used in the tables:

- **Attesters unsatisfied:** the share of attesters whose required inclusion-list transactions are not all among the transactions of the proposer's view. Those attesters would consider a block built from the proposer's view as not satisfying the inclusion lists.
- **Worst run:** the maximum of that share over all runs.
- **≥ 1/3:** counts runs where at least a third of attesters are unsatisfied. That is enough to stop the block from being justified.

## Findings

1. **Honest lists arrive in time.** With every member honest, the last node receives each list about 240 ms after it is published, averaged over runs. Packet loss up to 30% or a fourfold larger network adds a few tens of milliseconds to that tail. Making every uplink slow adds about 360 ms. Even then, the last node has each list roughly 0.4 s before the 9 s freeze.
2. **The proposer's 11 s cutoff prevents disagreement from late lists.** A member publishing late splits the attesters: those that received the list before 9 s count it, the others do not. The proposer keeps listening until 11 s, so it had the list in every run, and no attester was ever unsatisfied.
3. **Disagreement appears when the proposer also freezes at 9 s.** That is how `on_inclusion_list` stores lists, and the `late-publisher-strict` experiment models it. A member that publishes about 150 ms before the deadline then splits proposer and attesters. In one run in 50, 79% of attesters would have rejected the block.
4. **Equivocation is harmless only because the second list is forwarded.** Gossip forwards the second distinct list from a member, so the evidence reaches every node within a few hundred milliseconds and the equivocator is ignored everywhere. Version B sent after 10.8 s may reach the proposer only after its cutoff. The proposer then still counts the member while attesters do not, but attesters' requirements only shrink, so blocks stay valid.
5. **Accepting only the first list breaks this.** In `equivocation-first-only`, no evidence spreads when both versions go out at once. Each node keeps whichever version arrived first, and in the worst run 24% of attesters would reject the block.
6. **Most bandwidth is mesh amplification.** Each node sends about 1.16 MB per slot for 16 lists of about 8 KB, or roughly 7 copies of every list. IDONTWANT removes about 6% of it. It is sent only after a message arrives, and by then most duplicates are already in flight.

#### baseline

| runs | honest lists at freeze | p50 ms | p99 ms | max ms | KB sent / node | duplicates / node | attesters unsatisfied |
|---|---|---|---|---|---|---|---|
| 50 | 100.0% | 137 | 221 | 241 | 1157 | 118.9 | 0.0% |

#### late-publisher

| published at | nodes with it by 9 s | attesters counting it | proposer counts it | attesters unsatisfied (mean) | (worst run) | runs with ≥ 1/3 unsatisfied |
|---|---|---|---|---|---|---|
| 8s | 100.0% | 100.0% | 100.0% | 0.0% | 0.0% | 0.0% |
| 8.6s | 100.0% | 100.0% | 100.0% | 0.0% | 0.0% | 0.0% |
| 8.7s | 100.0% | 100.0% | 100.0% | 0.0% | 0.0% | 0.0% |
| 8.75s | 99.5% | 99.5% | 100.0% | 0.0% | 0.0% | 0.0% |
| 8.8s | 94.1% | 94.1% | 100.0% | 0.0% | 0.0% | 0.0% |
| 8.85s | 66.8% | 66.8% | 100.0% | 0.0% | 0.0% | 0.0% |
| 8.9s | 22.5% | 22.5% | 100.0% | 0.0% | 0.0% | 0.0% |
| 8.95s | 1.3% | 1.3% | 100.0% | 0.0% | 0.0% | 0.0% |
| 9s | 0.0% | 0.0% | 100.0% | 0.0% | 0.0% | 0.0% |
| 9.5s | 0.0% | 0.0% | 100.0% | 0.0% | 0.0% | 0.0% |

#### late-publisher-strict

| published at | nodes with it by 9 s | attesters counting it | proposer counts it | attesters unsatisfied (mean) | (worst run) | runs with ≥ 1/3 unsatisfied |
|---|---|---|---|---|---|---|
| 8s | 100.0% | 100.0% | 100.0% | 0.0% | 0.0% | 0.0% |
| 8.6s | 100.0% | 100.0% | 100.0% | 0.0% | 0.0% | 0.0% |
| 8.7s | 100.0% | 100.0% | 100.0% | 0.0% | 0.0% | 0.0% |
| 8.75s | 99.5% | 99.5% | 100.0% | 0.0% | 0.0% | 0.0% |
| 8.8s | 94.1% | 94.1% | 94.0% | 0.0% | 0.0% | 0.0% |
| 8.85s | 66.8% | 66.8% | 76.0% | 1.6% | 79.0% | 2.0% |
| 8.9s | 22.5% | 22.5% | 28.0% | 0.9% | 29.2% | 0.0% |
| 8.95s | 1.3% | 1.3% | 2.0% | 0.1% | 1.8% | 0.0% |
| 9s | 0.0% | 0.0% | 0.0% | 0.0% | 0.0% | 0.0% |
| 9.5s | 0.0% | 0.0% | 0.0% | 0.0% | 0.0% | 0.0% |

#### equivocation

| version B at | detected by 9 s | detected by check | attesters counting it | proposer counts it | attesters unsatisfied (mean) | (worst run) |
|---|---|---|---|---|---|---|
| 8.5s | 100.0% | 100.0% | 0.0% | 0.0% | 0.0% | 0.0% |
| 8.9s | 20.6% | 100.0% | 0.0% | 0.0% | 0.0% | 0.0% |
| 9.5s | 0.0% | 100.0% | 0.0% | 0.0% | 0.0% | 0.0% |
| 10.5s | 0.0% | 100.0% | 0.0% | 0.0% | 0.0% | 0.0% |
| 10.8s | 0.0% | 100.0% | 0.0% | 6.0% | 0.0% | 0.0% |
| 10.9s | 0.0% | 100.0% | 0.0% | 76.0% | 0.0% | 0.0% |
| 11s | 0.0% | 100.0% | 0.0% | 100.0% | 0.0% | 0.0% |
| 12s | 0.0% | 100.0% | 0.0% | 100.0% | 0.0% | 0.0% |
| 12.9s | 0.0% | 20.6% | 79.4% | 100.0% | 0.0% | 0.0% |

#### equivocation-first-only

| version B at | detected by 9 s | detected by check | attesters counting it | proposer counts it | attesters unsatisfied (mean) | (worst run) |
|---|---|---|---|---|---|---|
| 8.5s | 0.0% | 0.0% | 100.0% | 100.0% | 11.3% | 23.7% |
| 8.9s | 0.0% | 0.0% | 100.0% | 100.0% | 0.0% | 0.0% |
| 9.5s | 0.0% | 0.0% | 100.0% | 100.0% | 0.0% | 0.0% |
| 10.9s | 0.0% | 0.0% | 100.0% | 100.0% | 0.0% | 0.0% |

#### lossy-links

| value | honest lists at freeze (worst) | p50 ms | p99 ms | max ms | KB sent / node | duplicates / node |
|---|---|---|---|---|---|---|
| 0 | 100.0% | 137 | 221 | 241 | 1157 | 118.9 |
| 0.01 | 100.0% | 138 | 221 | 242 | 1156 | 117.5 |
| 0.05 | 100.0% | 140 | 224 | 246 | 1154 | 111.8 |
| 0.1 | 100.0% | 143 | 228 | 251 | 1152 | 104.9 |
| 0.2 | 100.0% | 150 | 238 | 266 | 1147 | 91.0 |
| 0.3 | 99.9% | 160 | 252 | 288 | 1143 | 77.3 |

#### slow-uplinks

| value | honest lists at freeze (worst) | p50 ms | p99 ms | max ms | KB sent / node | duplicates / node |
|---|---|---|---|---|---|---|
| 0 | 100.0% | 137 | 221 | 241 | 1157 | 118.9 |
| 0.25 | 100.0% | 181 | 270 | 299 | 1170 | 120.5 |
| 0.5 | 100.0% | 226 | 334 | 395 | 1181 | 121.8 |
| 0.75 | 100.0% | 294 | 443 | 502 | 1179 | 121.5 |
| 1 | 100.0% | 379 | 544 | 599 | 1158 | 119.0 |

#### network-size

| value | honest lists at freeze (worst) | p50 ms | p99 ms | max ms | KB sent / node | duplicates / node |
|---|---|---|---|---|---|---|
| 250 | 100.0% | 112 | 194 | 206 | 1179 | 121.8 |
| 500 | 100.0% | 125 | 209 | 224 | 1165 | 119.9 |
| 1000 | 100.0% | 138 | 221 | 242 | 1156 | 118.8 |
| 2000 | 100.0% | 149 | 233 | 258 | 1154 | 118.5 |
| 4000 | 100.0% | 160 | 243 | 273 | 1150 | 118.1 |

#### idontwant

| value | honest lists at freeze (worst) | p50 ms | p99 ms | max ms | KB sent / node | duplicates / node |
|---|---|---|---|---|---|---|
| true | 100.0% | 150 | 234 | 254 | 1148 | 117.8 |
| false | 100.0% | 151 | 235 | 256 | 1216 | 126.9 |

#### withholding

| silent members | lists in proposer view | KB sent / node | attesters unsatisfied |
|---|---|---|---|
| 0 | 16.0 | 1157 | 0.0% |
| 1 | 15.0 | 1085 | 0.0% |
| 4 | 12.0 | 867 | 0.0% |
| 8 | 8.0 | 578 | 0.0% |
| 12 | 4.0 | 289 | 0.0% |
| 15 | 1.0 | 73 | 0.0% |
