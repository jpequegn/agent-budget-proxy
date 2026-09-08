# Architecture and trust boundaries

```text
Operator credential -> issue run -> minted capability + immutable IDs
Agent capability -> strict JSON -> catalog/policy -> approval if needed
                                                   |
Reviewer credential ------------------------------+
                                                   v
SQLite immediate transaction: ancestors + budgets + rate buckets + audit
                                                   |
                                          durable reservation
                                                   v
                                           trusted mock tool
                                                   |
SQLite immediate transaction: actual outcome + release hold + audit
                                                   |
                                          report / billing export

OpenTelemetry observes these stages; it cannot authorize a dispatch.
Replay rebuilds policy/accounting in a temporary ledger and never calls a tool.
```

## Authority

The operator is trusted to allocate independent root budgets. Each root mints an agent and session; child runs inherit both and cannot widen scope or locality. Costs, tokens, write attempts, and rate limits consume every ancestor's authority. Root issuance is not an account-wide budget controller.

The agent receives only a scoped bearer capability, stored hashed. The HTTP schema does not accept identity, quote, or settlement fields. The `Tool` interface and internal `settle` method belong to trusted server code, never agent input. Unknown verbs/resources fail closed, including unknown reads; `inspect` is the explicit safe read-only route.

Reviewer credentials are distinct from operator and agent credentials. Approval binds request fingerprint, effective bounds, quote, policy, and action ID, and expires within two minutes. A duplicate request cannot change the approved payload. Final reservation repeats budget checks after approval.

## State machine

An action begins as denied, awaiting approval, or executing. Only a newly committed executing action dispatches. Known outcomes become settled or overrun. Uncertain dispatch or failed settlement leaves executing state and holds in place. There is no lease expiry that silently frees spend, and restart cannot replay the side effect.

The deletion-hour window is a fixed window anchored at run creation/reset, not a rolling window. Failed-write counts reset with that window. Writes count attempted units even on failure. Clock regression cannot refill a token bucket, and reservation rejects time before the last successful reservation. Capability expiry blocks new actions, but trusted settlement can still record a late result.

## Persistence

`state` holds a versioned bounded JSON object. `audit` stores hash-linked changes and the resulting state hash in the same immediate SQLite transaction. Each operation verifies the audit chain and current state before mutation or export. Two separately opened database connections cannot reserve the same remaining funds. This simple whole-state model is intentionally limited in size.

Policy and resource snapshots accompany decisions. Events capture created runs and changed actions. Replay remaps generated identities, reapplies the recorded clock and policy, and uses recorded outcomes to check decisions and final balances. This verifies the saved control logic, not provider honesty or model quality. Replay uses a stopped/quiescent source ledger; do not replay one that another process is changing.

## Failure behavior

| Failure | Behavior |
| --- | --- |
| Ledger unavailable/corrupt before reservation | No dispatch |
| Ledger unavailable after dispatch | Keep outstanding hold; no redispatch |
| Provider uncertainty | Keep outstanding hold; no redispatch |
| Known partial failure | Charge actual cost, release unused money/tokens, retain attempted writes |
| Trusted outcome exceeds quote | Record actual usage, freeze run and ancestors |
| Collector/export failure | Continue ledger enforcement; surface exporter error |
| Policy unknown/flagged/out of region | Explainable denial, no dispatch |
| Approval missing/expired/changed | No dispatch |

Local trace output is capped at 10,000 spans per exporter lifetime. Optional OTLP uses a bounded 256-span queue and one-second export timeout with retries disabled. Local file export is synchronous; a pathological blocking filesystem can delay execution, so the fail-open guarantee covers export errors, not arbitrary filesystem hangs.
