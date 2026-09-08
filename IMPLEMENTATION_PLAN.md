# Implementation plan

Source: https://github.com/jpequegn/project-ideas/issues/245

1. Scaffold Go project, CI and explicit domain contracts: Create Go tooling, strict JSON contracts for run/action/budget/decision/outcome, integer micro-dollar units and deterministic helpers. Pin dependencies and document the eight-task plan. Verify go test, go vet and formatting.

2. Implement transactional SQLite ledger and immutable run identity: Mint agent/session/run IDs and capability secrets on the server. Persist state plus an atomic hash-linked audit trail. Bind short-lived capabilities to scoped runs, support child runs sharing ancestor limits, and test reopen, tampering, identity and ledger failures.

3. Enforce atomic reservations, settlement and refillable limits: Implement estimate/reserve/settle, concurrent-safe cumulative budgets and verb/run token buckets. Charge failures and hold uncertain in-flight work across restart. Make idempotency keys payload-bound. Test concurrent reservations, retries, parent limits, clock skew and invalid settlement.

4. Add resource catalog, steering and second-key policies: Create three mock paid tools and three infrastructure options plus safe inspection/deletion fixtures. Enforce locality, data class, scope, availability, blocked counterparties, reversibility and aggregate write trip wires. Implement budget-safe model fallback, bounded-output steering and independent action-bound expiring approvals.

5. Build governed execution with failure and crash-safe mock tools: Connect reservation to a deterministic mock executor, settling actual tokens/cost/latency/outcomes. Never replay uncertain side effects. Test outage/fallback, partial failure, settlement outage, overrun quarantine and no tool execution when ledger/policy denies.

6. Expose authenticated HTTP API and operator CLI: Add loopback net/http endpoints and Cobra commands for run issuance, action execution, second-key approval and inspection. Separate agent/admin/reviewer credentials; prohibit caller identity and price overrides. Add CLI demo and HTTP integration tests.

7. Add telemetry, replay and incident comparison reports: Instrument stages with OpenTelemetry, local and optional OTLP export. Implement deterministic synthetic incident replay and comparison of request caps, steering and second-key control; report cost per accepted outcome and prevented effects. Document and test collector failure versus ledger failure.

8. Finish adversarial verification and project guide: Complete race/property/adversarial tests, restart/replay verification, example billing boundary, optional container setup and fresh-checkout checks. Commit usage, capabilities, limitations, learning exercises and innovative extensions. Close source only after all task PRs pass CI and merge.

## Scope decisions

Go 1.25.5 with net/http JSON and Cobra, SQLite immediate transactions, and explicit Go policy rules. Money uses integer micro-dollars. Trusted mock resources only; no arbitrary upstream URLs or actual payments. Child runs share ancestor budgets. Uncertain executions keep reservations and are never retried automatically. Telemetry is secondary to the durable ledger. gRPC, Redis and WASM policies are later extensions.

