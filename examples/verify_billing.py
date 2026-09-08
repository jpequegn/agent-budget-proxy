"""Run with metered-billing-sandbox installed; creates only a temporary database."""

import json
import sys
import tempfile
from pathlib import Path

from metered_billing.ledger import LedgerStore
from metered_billing.models import UsageEvent


def main():
    events = [UsageEvent.model_validate(item) for item in json.load(sys.stdin)]
    if not events:
        raise SystemExit("Expected at least one settled usage event")
    with tempfile.TemporaryDirectory() as directory:
        with LedgerStore(Path(directory) / "billing.db") as store:
            assert store.ingest_usage_events(events) == len(events)
            assert store.ingest_usage_events(events) == 0
            assert store.usage_count() == len(events)
    print(json.dumps({"validated_and_ingested": len(events), "duplicate_inserts": 0,
                      "cost_micros": sum(e.metadata["cost_micros"] for e in events),
                      "invoices_created": 0}))


if __name__ == "__main__":
    main()
