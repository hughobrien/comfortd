#!/usr/bin/env python3
"""THROWAWAY phase-0 spike: dump raw Kumo Cloud JSON for the API inventory.

Read-only (GET only, plus login). Credentials come from env vars and are never
written to disk. Output goes to spike/out/ (gitignore it); tokens are not saved.

    export KUMO_USER='you@example.com'
    read -rs KUMO_PASS; export KUMO_PASS
    python3 -I spike/dump.py
"""
import json
import os
import pathlib
import sys
import urllib.error
import urllib.request

BASE = "https://app-prod.kumocloud.com/v3"
APP_VERSION = "3.2.4"  # from comfort_HA; may need bumping
OUT = pathlib.Path(__file__).resolve().parent / "out"


def call(method, path, token=None, body=None):
    headers = {"x-app-version": APP_VERSION, "Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.status, json.loads(r.read() or b"null")
    except urllib.error.HTTPError as e:
        return e.code, {"error": e.read().decode(errors="replace")[:500]}


def save(name, obj):
    OUT.mkdir(exist_ok=True)
    (OUT / f"{name}.json").write_text(json.dumps(obj, indent=2, sort_keys=True))
    print(f"wrote out/{name}.json")


def main():
    user, pw = os.environ.get("KUMO_USER"), os.environ.get("KUMO_PASS")
    if not user or not pw:
        sys.exit("set KUMO_USER and KUMO_PASS")

    status, login = call("POST", "/login", body={"username": user, "password": pw, "appVersion": APP_VERSION})
    if status != 200:
        sys.exit(f"login failed: {status}")
    token = login["token"]["access"]
    # Keep only non-secret login fields (user profile), never tokens.
    save("login_user", {k: v for k, v in login.items() if k != "token"})

    for name, path in [("account", "/accounts/me"), ("sites", "/sites/")]:
        s, d = call("GET", path, token)
        save(name, {"status": s, "data": d})

    _, sites = call("GET", "/sites/", token)
    for site in sites if isinstance(sites, list) else []:
        sid = site["id"]
        s, zones = call("GET", f"/sites/{sid}/zones", token)
        save(f"zones_{sid}", {"status": s, "data": zones})
        for z in zones if isinstance(zones, list) else []:
            adapter = z.get("adapter") or {}
            serial = adapter.get("deviceSerial")
            if not serial:
                continue
            for suffix in ["", "/profile", "/status", "/sensor"]:
                s, d = call("GET", f"/devices/{serial}{suffix}", token)
                save(f"device_{serial}{suffix.replace('/', '_')}", {"status": s, "data": d})
            s, d = call("GET", f"/zones/{z['id']}/notification-preferences", token)
            save(f"zone_{z['id']}_notifications", {"status": s, "data": d})


if __name__ == "__main__":
    main()
