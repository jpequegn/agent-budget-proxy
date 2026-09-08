"""Exercise the built CLI over real HTTP and stop the server afterwards."""

import json
import os
from pathlib import Path
import secrets
import select
import signal
import subprocess
import sys
import tempfile
from urllib.error import HTTPError
from urllib.request import Request, urlopen


def main():
    binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else "./abp").resolve())
    admin, reviewer = secrets.token_hex(24), secrets.token_hex(24)
    env = dict(os.environ, ABP_ADMIN_TOKEN=admin, ABP_REVIEW_TOKEN=reviewer)
    env.pop("ABP_OTLP_ENDPOINT", None)
    with tempfile.TemporaryDirectory() as directory:
        process = subprocess.Popen(
            [binary, "serve", "--db", f"{directory}/ledger.db", "--listen", "127.0.0.1:0"],
            env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        try:
            # The server prints its OS-assigned port after binding the listener.
            ready, _, _ = select.select([process.stdout], [], [], 10)
            if not ready:
                raise RuntimeError("Server did not start within 10 seconds")
            line = process.stdout.readline().strip()
            if not line.startswith("Listening on http://127.0.0.1:"):
                raise RuntimeError(f"Unexpected startup: {line}")
            base = line.removeprefix("Listening on ")

            def call(path, token="", body=None, expected=200):
                data = None if body is None else json.dumps(body).encode()
                request = Request(base + path, data=data, headers={
                    "Authorization": f"Bearer {token}", "Content-Type": "application/json"})
                try:
                    response = urlopen(request, timeout=5)
                except HTTPError as error:
                    response = error
                with response:
                    payload = json.load(response)
                    assert response.status == expected, (response.status, payload)
                    return payload

            assert call("/health")["status"] == "ok"
            run = call("/runs", admin, {
                "budget": {"cost_micros": 100000, "tokens": 10000, "writes": 5,
                           "seconds": 600, "burst": 20, "refill_per_second": 2},
                "scope": ["inspect", "delete"], "region": "us"}, 201)
            cap = run["capability"]
            req = {"key": "inspect-1", "verb": "inspect", "resource": "inspect",
                   "units": 1, "data_class": "public"}
            first = call("/actions", cap, req)
            assert call("/actions", cap, req)["id"] == first["id"]
            deletion = dict(req, key="delete-1", verb="delete", resource="delete")
            pending = call("/actions", cap, deletion, 202)
            approval = {"id": pending["id"], "digest": pending["approval_digest"]}
            call("/approvals", cap, approval, 401)
            call("/approvals", reviewer, approval)
            done = call("/actions", cap, deletion)
            assert done["state"] == "settled" and done["outcome"]["effects"] == 1
            snapshot = call("/run", cap)
            assert len(snapshot["actions"]) == 2
            assert snapshot["run"]["spent_micros"] == 75
            print("HTTP smoke passed: identity, inspection, idempotency, separate reviewer, settlement")
        finally:
            process.send_signal(signal.SIGTERM)
            try:
                process.communicate(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.communicate()
                raise
        if process.returncode != 0:
            raise RuntimeError(f"Server exited {process.returncode}")


if __name__ == "__main__":
    main()
