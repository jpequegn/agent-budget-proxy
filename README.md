# Agent Budget Proxy

A local Go lab for governing an agent's whole run: scoped identity, shared parent/child budgets, atomic spend reservations, request steering, and separate reviewer approval. SQLite records the decisions and outcomes; replay checks them without calling tools.

All models, paid tools, deletions, and infrastructure resources are synthetic. No API keys, payments, cloud accounts, or real side effects are needed. This does not control your Codex or ChatGPT subscription usage.

## Try it

Requires Go 1.25.5 or newer.

```sh
git clone https://github.com/jpequegn/agent-budget-proxy.git
cd agent-budget-proxy
go build -o abp ./cmd/abp
./abp demo --out data/first-demo
./abp replay --db data/first-demo/ledger.db
./abp experiment --out reports/first-comparison
```

Use a new output directory on each run. The demo prints `spent=9075 micros held=0 micros completed=3 blocked_or_paused=2` and writes a ledger, report, and local traces. One dollar is 1,000,000 micro-dollars. The demo explicitly simulates a reviewer approving one deletion.

The experiment compares request caps, budget steering, and second-key controls. Read `reports/first-comparison/comparison.md` for the completion/spend tradeoff, not just the cheapest result.

```sh
./abp serve --db data/interactive.db
```

The HTTP API binds to `127.0.0.1:4320`. Startup prints separate ephemeral operator and reviewer credentials. Keep the reviewer credential out of the agent process. Stop with Ctrl-C. There is no browser dashboard; use the CLI reports or HTTP requests in [the project guide](PROJECT_GUIDE.md).

## Verify

```sh
go test -race ./...
go vet ./...
python3 examples/api_smoke.py ./abp
```

The smoke test uses a temporary database and loopback port, then stops its server. See [evaluation](docs/EVALUATION.md) for measured synthetic results and [architecture](docs/ARCHITECTURE.md) for trust boundaries.

Source idea: [project-ideas #245](https://github.com/jpequegn/project-ideas/issues/245). The [implementation plan](IMPLEMENTATION_PLAN.md) records the eight-task scope.
