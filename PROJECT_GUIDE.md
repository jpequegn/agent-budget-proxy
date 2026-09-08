# Project guide

## What it can do

- Mint agent/session/run identities and short-lived bearer capabilities. Agents cannot supply identities, prices, or reviewer names in HTTP requests.
- Check verb scope, data locality, data class, catalog availability, reversibility, risk, and flagged counterparties before dispatch.
- Reserve worst-case synthetic cost and tokens atomically, then settle actual usage, including known failed calls. Parent and child runs share ancestor budgets.
- Enforce money, token, write-attempt, wall-clock, run-rate, and verb-rate limits. Trip wires stop deletion storms and repeated failed writes.
- Select a cheaper model or bound output when funds run low. Steering records compact-context and brevity instructions; mocks do not actually summarize prompts.
- Pause high-risk work for a distinct reviewer credential. Approval expires and binds the action, effective request, policy, and quote. Approval never bypasses the final budget check.
- Preserve uncertain calls across restart without dispatching them again. Export local OpenTelemetry traces, replay saved decisions, and export settled usage to the billing sandbox.

## Explore the demo

Run the README quickstart, then inspect the complete report:

```sh
./abp inspect --db data/first-demo/ledger.db | jq '.actions[] | {request, effective, decision, state, outcome}'
```

Compare `request.resource` with `effective.resource` to see a model downgrade. The single deletion has a reviewer; the broad deletion is denied by `delete_tripwire`; the flagged paid tool is blocked by `counterparty_hold`. A compliance hold is terminal for that request key in V1 and cannot be waived by the reviewer endpoint.

Report fields `spent_micros`, `held_micros`, and `reserved_micros` distinguish settled cost from exposure still in flight. Counts of writes are attempted units, conservatively consumed even if the provider later reports partial failure. Root-run spend already includes children, so do not sum every run to compute a total.

## Use the HTTP API

Start `./abp serve --db data/interactive.db`. In another terminal set `ADMIN` and `REVIEWER` to the separately printed credentials. `jq` is needed for these shell examples. You can also set `ABP_ADMIN_TOKEN` and `ABP_REVIEW_TOKEN` before startup to keep credentials across restarts. Both must be distinct and at least 24 characters. Do not commit them or grant the agent access to both.

```sh
BASE=http://127.0.0.1:4320
RUN=$(curl -fsS "$BASE/runs" -H "Authorization: Bearer $ADMIN" \
  -H 'Content-Type: application/json' -d '{"budget":{"cost_micros":100000,"tokens":10000,"writes":5,"seconds":600,"burst":20,"refill_per_second":2},"scope":["inspect","model","delete","paid_tool","provision"],"region":"us"}')
CAP=$(printf '%s' "$RUN" | jq -r .capability)
curl -fsS "$BASE/actions" -H "Authorization: Bearer $CAP" \
  -H 'Content-Type: application/json' -d '{"key":"read-1","verb":"inspect","resource":"inspect","units":1,"data_class":"public"}' | jq
```

The same action key and payload return the existing action; a changed payload conflicts. Keys belong to the run. Denied and expired actions need a new key after the underlying problem is resolved. Never use a new key to retry an uncertain side effect.

Try a deletion and approve it from the reviewer's terminal:

```sh
ACTION='{"key":"delete-1","verb":"delete","resource":"delete","units":1,"data_class":"internal"}'
PENDING=$(curl -fsS "$BASE/actions" -H "Authorization: Bearer $CAP" \
  -H 'Content-Type: application/json' -d "$ACTION")
printf '%s' "$PENDING" | jq '{state, decision, estimated_cost_micros, approval_until}'
APPROVAL=$(printf '%s' "$PENDING" | jq '{id, digest:.approval_digest}')
curl -fsS "$BASE/approvals" -H "Authorization: Bearer $REVIEWER" \
  -H 'Content-Type: application/json' -d "$APPROVAL" | jq
curl -fsS "$BASE/actions" -H "Authorization: Bearer $CAP" \
  -H 'Content-Type: application/json' -d "$ACTION" | jq
```

Approval alone does not dispatch. Resubmit the unchanged request within 120 seconds and before run expiry. The demo scripts simulate separate credentials; production would need real reviewer authentication and organizational controls.

| Endpoint | Authority | Purpose |
| --- | --- | --- |
| `GET /health` | None, loopback | Ledger integrity/availability |
| `POST /runs` | Operator | Issue a root run |
| `POST /children` | Parent capability | Issue a child with `parent` ID, subset scope, same region |
| `GET /run` | Run capability | Own run and actions |
| `POST /actions` | Run capability | Govern and execute mock action |
| `GET /approvals` | Reviewer | Pending approvals, including expired entries |
| `POST /approvals` | Reviewer | Approve exact action ID/digest |

Actions return 200 settled, 202 pending or uncertain, 422 denied, and 503 when dispatch/settlement is unavailable or uncertain. Invalid JSON is 400. A run capability rejected because the ledger cannot be read returns 403; check `/health` for the availability distinction. Expired capabilities cannot inspect the API; the local operator can still use `abp inspect`.

Catalog resource IDs are `inspect`, `delete`, `model-premium`, `model-small`, `paid-search`, `paid-archive`, `paid-flagged`, `db-small`, `compute-small`, and `storage-eu`. Scope verbs are `inspect`, `delete`, `model`, `paid_tool`, and `provision`. `max_tokens` is required for model calls and must be zero or absent elsewhere. Catalog and policy configuration live in `internal/proxy/policy.go`; restart after changing them. Hot reload is not supported.

## Billing integration

`billing-export` emits one usage event per settled action, including charged failures and overruns. It omits pending/uncertain actions rather than guessing a bill. Export IDs are stable, so ingestion can deduplicate. Quantity is one action; exact cost remains `metadata.cost_micros`. The billing sandbox's prices use minor currency units, so this integration deliberately does not invent an invoice conversion or charge a wallet.

Install the separate sandbox in an adjacent checkout, then validate the exported events against its real schema and SQLite ingestion code:

```sh
git clone https://github.com/jpequegn/metered-billing-sandbox.git ../metered-billing-sandbox
uv sync --project ../metered-billing-sandbox --frozen --no-dev
./abp billing-export --db data/first-demo/ledger.db --customer local_lab | \
  ../metered-billing-sandbox/.venv/bin/python examples/verify_billing.py
```

The check creates a temporary billing database, ingests the batch twice, and confirms the second insert count is zero. See the recorded compatible revision in [evaluation](docs/EVALUATION.md). This is an export/ingestion boundary, not a live billing authorization adapter.

## Telemetry and containers

Local traces are `traces.jsonl` in a demo output directory or `<db>.traces.jsonl` for the server. `ABP_OTLP_ENDPOINT=http://127.0.0.1:4318` additionally enables OTLP/HTTP export to your collector. Leave it unset for an entirely local run. Only use an endpoint you trust; traces contain run IDs and decision metadata, but no bearer capabilities or request bodies.

Compose passes `demo --wait-collector` to wait up to ten seconds for the collector's TCP listener before starting. This opt-in demo readiness check is separate from normal fail-open telemetry; the interactive server never depends on collector readiness.

```sh
docker compose up --build --abort-on-container-exit --exit-code-from demo
docker compose down
```

Compose runs the in-process mock agent/proxy demo with a collector on an internal network; it exposes no host ports. Container evidence lives in `/tmp/demo` inside the demo container until removed. The interactive HTTP server remains loopback-only and is intended to run directly on the host. No public deployment is configured.

## Practical uses

Use this to rehearse an agent-tool integration before giving it a paid API or write credential. Change the role's scope and budget, inject provider outages, inspect the decisions, and check whether useful read-only work survives. For Castflow, an eventual adapter could give each podcast enrichment run a budget and preserve outstanding holds when a provider times out. That adapter is not included here.

Another useful pattern is incident review: keep a ledger from a bad run, replay the decisions without repeating the writes, then compare a changed policy on the synthetic incident. Use successful outcomes and write effects alongside spend. The cheapest policy can simply be the one that permits the least work.

## Learning exercises and extensions

1. Trace one request through `Begin`, `reserveState`, `Execute`, and `settle`. Try a retry before and after settlement; explain why only one dispatch occurs.
2. Lower the operator-issued budget and compare premium-model requests. Change model prices or availability, then run the experiment and its tests. Price changes invalidate outstanding approvals.
3. Create parent/child runs with different scopes. Spend from the child and inspect the parent. Add a test for a sibling race.
4. Inject `MockTool.Uncertain` and restart the store. Design a reconciliation protocol using a provider receipt before attempting to release a hold. Never release it just because a timeout elapsed.
5. Add a trusted provider adapter with bounded quotes, provider-side idempotency, and independent outcome verification. Keep agents from supplying the upstream URL or selecting the implementation.
6. Feed incident records into the [Clinical Case Memory Eval Lab idea #244](https://github.com/jpequegn/project-ideas/issues/244). Evaluate whether recovered incident knowledge prevents repeat failures without blanket denials.
7. Build a policy tuner that searches for the best successful-outcome/spend tradeoff on held-out incidents. Freeze the incident fixtures and include adversarial approval and fallback cases to avoid optimizing only the demo.
8. Add account/project-wide authority, authenticated reviewer identities, reconciliation, and provider billing receipts before considering live infrastructure procurement. Redis, gRPC, WASM policies, and a value-per-outcome dashboard are separate extensions.

## Limits that matter

This is a bounded teaching service with 100 runs, 1,000 actions, 5,000 events, and 4 MiB serialized state. SQLite serializes writes; full audit verification before each read/write favors correctness over throughput. Start a new ledger for a new lab. Do not discard unresolved holds from a real adapter.

Known outcomes may exceed a trusted quote only in fault-injection/custom-adapter tests. The ledger records the true overrun and freezes the run and ancestors; it does not clip charges. A hard spend guarantee requires an actually bounded provider contract. Write budgets count attempts, not a promise that an untrusted tool obeys them.

Uncertain dispatch remains `executing` with retained reservations. There is no reconciliation or approval-rejection API yet. Stop the server before taking a forensic replay/export snapshot. V1 does not migrate development ledgers created before policy snapshots and explicit bucket initialization.

The hash-linked ledger detects accidental changes and partial tampering, not an owner who rewrites the whole database and hashes. Keep the database, WAL files, and operator credentials private. Separate bearer tokens are not proof of two distinct humans. No live LLM, real compliance screening, payments, network provisioning, or actual prompt compaction is implemented.
