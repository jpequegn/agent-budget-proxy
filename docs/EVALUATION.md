# Evaluation

## Reproduce

```sh
go test -race ./...
go vet ./...
go build -o abp ./cmd/abp
python3 examples/api_smoke.py ./abp
./abp demo --out data/evaluation-demo
./abp replay --db data/evaluation-demo/ledger.db
./abp experiment --out reports/evaluation-incident
```

CI runs those checks on Linux with Go 1.25.5. Development checks also ran on macOS arm64. The HTTP and collector-outage tests need permission to bind temporary loopback ports. No paid provider is contacted.

Docker Compose was also built and run locally with collector 0.133.0. The corrected readiness sequence exported 18 spans successfully, and the demo exited zero with the same accounting totals. Both containers and their network were removed after verification.

## Synthetic incident results

The fixed workload includes inspections, repeated model work, a single reviewed deletion, a broad deletion storm, a flagged paid tool, and a provider outage. The baseline retains catalog controls but effectively removes cumulative constraints; it is a teaching comparison, not a performance benchmark against a production gateway.

| Policy | Spent, micro-dollars | Successful mock outcomes | Successful inspections | Synthetic write effects | Blocked/paused |
| --- | ---: | ---: | ---: | ---: | ---: |
| Request caps | 191325 | 10 | 3 | 151 | 2 |
| Steering | 60975 | 8 | 3 | 1 | 4 |
| Second key | 6075 | 5 | 3 | 1 | 7 |

All three ledgers replay successfully. Steering prevents 150 write effects relative to the caps baseline while retaining every inspection. The second-key mode spends less but completes fewer actions because approvals are withheld. Counts are from a deterministic mock success flag, not independently graded LLM answers. Cost per successful mock outcome is 19132.5, 7621.875, and 1215 micro-dollars respectively. Random IDs, hashes, and measured latency vary across executions.

The smaller demo settles 9,075 micro-dollars with zero outstanding holds, three completed actions, and two denials. Its billing export contains three usage events. Verification against `metered-billing-sandbox` revision `01bbb18f946472c15317d2bffa570bf2a521ebbc` validates the real Pydantic schema, ingests all three into SQLite, and inserts zero on a second pass. It preserves the 9,075 micro-dollar total in metadata and creates no invoice.

## Adversarial coverage

- Strict JSON, unknown fields, oversized input, forged/expired capabilities, parent identity, and scope/locality escalation.
- Concurrent reservations from one store and from separate SQLite connections; randomized bounded outcomes preserve held/spent conservation.
- Epoch-zero and regressing-clock token buckets, parent/child accounting, retry fingerprints, and repeated settlement.
- Action-bound second keys, expiry, changed policy/quote, failed-write trip wire, deletion storm, quota, data class, and provider fallback.
- Known failure charging, unknown outcome after dispatch, restart without duplicate effects, ledger failure before/after dispatch, and overrun quarantine.
- Audit corruption on reopen and live corruption before another dispatch. Exports and health checks also verify integrity.
- Real HTTP CLI smoke: run issuance, inspection, idempotency, rejection of agent approval, reviewer approval, and settlement. Server exits after the test.
- Local trace-write failure and a real HTTP collector returning 503. Ledger settlement remains correct and trace output excludes the bearer capability.
- Replay of all three policy modes and stable billing export with charged failures but no uncertain holds.

## Remaining evidence gaps

No live provider pricing, quality, cancellation, or provider-side idempotency has been tested. No production throughput, malicious filesystem owner, multi-host coordination, real human separation, or long-term storage migration guarantee is claimed. The Docker setup is a local demo, not deployment hardening. Reconciliation for uncertain paid calls is required before adding real side effects.
