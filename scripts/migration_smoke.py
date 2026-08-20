"""V2 migration gate smoke helper.

The default mode is read-only. Mutating modes require explicit command-line
confirmation, and apply/rollback additionally require
V2_MIGRATION_ALLOW_DESTRUCTIVE=YES.
"""

import argparse
import http.cookiejar
import json
import os
import time
import urllib.error
import urllib.request


class Client:
    def __init__(self, base_url: str) -> None:
        self.base = base_url.rstrip("/")
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def request(self, method: str, path: str, body=None, mutation: bool = False):
        data = None if body is None else json.dumps(body).encode("utf-8")
        headers = {"Accept": "application/json"}
        if data is not None:
            headers["Content-Type"] = "application/json"
        if mutation:
            headers["Origin"] = self.base
            headers["X-VProxy-Action"] = "migration"
        request = urllib.request.Request(self.base + path, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=30) as response:
                return response.status, json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as error:
            payload = json.loads(error.read().decode("utf-8"))
            return error.code, payload

    def login(self, password: str) -> None:
        status, payload = self.request("POST", "/api/admin/login", {"password": password})
        if status != 200:
            raise RuntimeError(f"admin login failed: HTTP {status}: {payload}")


def wait_for_transition(client: Client, timeout: int = 300) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        status, payload = client.request("GET", "/api/admin/migration/status")
        state = payload.get("state", "") if isinstance(payload, dict) else ""
        if status == 404:
            health_status, health = client.request("GET", "/health")
            if health_status == 200 and health.get("status") != "migration_required":
                return
        if state in {"completed", "rolled_back", "failed_recoverable"}:
            if state == "failed_recoverable":
                raise RuntimeError(f"migration entered failed_recoverable: {payload}")
            return
        time.sleep(1)
    raise TimeoutError("migration transition did not finish before timeout")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default=os.environ.get("V2_BASE_URL", "http://127.0.0.1:2156"))
    parser.add_argument("--mode", choices=["status", "prepare", "apply", "rollback-prepare", "rollback-apply"], default="status")
    parser.add_argument("--confirm-mutation", action="store_true")
    args = parser.parse_args()

    password = os.environ.get("V2_ADMIN_PASSWORD", "")
    if not password:
        raise SystemExit("missing required environment variable: V2_ADMIN_PASSWORD")
    if args.mode != "status" and not args.confirm_mutation:
        raise SystemExit("mutating modes require --confirm-mutation")
    if args.mode in {"apply", "rollback-apply"} and os.environ.get("V2_MIGRATION_ALLOW_DESTRUCTIVE") != "YES":
        raise SystemExit("apply modes require V2_MIGRATION_ALLOW_DESTRUCTIVE=YES")

    client = Client(args.base_url)
    health_status, health = client.request("GET", "/health")
    client.login(password)
    status_code, status = client.request("GET", "/api/admin/migration/status")
    print(json.dumps({"health_http": health_status, "health": health, "status_http": status_code, "migration": status}, indent=2))
    if args.mode == "status":
        return

    rollback = args.mode.startswith("rollback")
    prepare_path = "/api/admin/migration/rollback/prepare" if rollback else "/api/admin/migration/prepare"
    prepare_status, plan = client.request("POST", prepare_path, {}, mutation=True)
    if prepare_status != 200:
        raise RuntimeError(f"prepare failed: HTTP {prepare_status}: {plan}")
    print(json.dumps(plan, indent=2))
    if args.mode.endswith("prepare"):
        return

    plan_hash = plan.get("plan_hash", "")
    if not plan_hash:
        raise RuntimeError("prepare response did not include plan_hash")
    if rollback:
        apply_path = "/api/admin/migration/rollback/apply"
        body = {"plan_hash": plan_hash, "v1_binary_confirmed": True, "traffic_stop_confirmed": True}
    else:
        apply_path = "/api/admin/migration/apply"
        body = {"plan_hash": plan_hash, "backup_confirmed": True, "rollback_understood": True}
    apply_status, accepted = client.request("POST", apply_path, body, mutation=True)
    if apply_status != 202:
        raise RuntimeError(f"apply failed: HTTP {apply_status}: {accepted}")
    wait_for_transition(client)
    print("migration transition completed")


if __name__ == "__main__":
    main()
