#!/usr/bin/env python3
"""THROWAWAY phase-0 write probe. Sets Office spHeat and polls state.

    { read -r u; read -r p; KUMO_USER=$u KUMO_PASS=$p python3 -I spike/probe_write.py 21; } < comfort
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

BASE = "https://app-prod.kumocloud.com/v3"
VER = "3.2.4"
ZONE_NAME = "Office"


def call(method, path, token=None, body=None):
    h = {"x-app-version": VER, "Content-Type": "application/json"}
    if token:
        h["Authorization"] = f"Bearer {token}"
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, headers=h, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            raw = r.read()
            return r.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")[:500]


def snap(d):
    rv = d.get("realValues") or {}
    return {k: d.get(k) for k in ("power", "operationMode", "spHeat", "spCool", "roomTemp", "updatedAt")} | {"realValues": rv}


LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "out", "probe_log.jsonl")


def log(**rec):
    os.makedirs(os.path.dirname(LOG), exist_ok=True)
    with open(LOG, "a") as f:
        f.write(json.dumps({"ts": time.strftime("%Y-%m-%dT%H:%M:%S%z"), **rec}) + "\n")


def main():
    commands = json.loads(sys.argv[1])  # e.g. '{"operationMode":"vent"}'
    _, login = call("POST", "/login", body={"username": os.environ["KUMO_USER"], "password": os.environ["KUMO_PASS"], "appVersion": VER})
    tok = login["token"]["access"]
    _, sites = call("GET", "/sites/", tok)
    _, zones = call("GET", f"/sites/{sites[0]['id']}/zones", tok)
    zone = next(z for z in zones if z["name"] == ZONE_NAME)
    serial = zone["adapter"]["deviceSerial"]

    _, before = call("GET", f"/devices/{serial}", tok)
    print("BEFORE", json.dumps(snap(before)))

    t0 = time.time()
    s, resp = call("POST", "/devices/send-command", tok, {"deviceSerial": serial, "commands": commands})
    print(f"SEND status={s} response={json.dumps(resp)[:600]}")
    log(kind="send", commands=commands, status=s, response=resp, before=snap(before))

    last = None
    while time.time() - t0 < 20:
        time.sleep(4)
        s, d = call("GET", f"/devices/{serial}", tok)
        cur = json.dumps(snap(d), sort_keys=True) if s == 200 else f"status {s}"
        if cur != last:
            print(f"t+{time.time() - t0:5.1f}s {cur}")
            log(kind="state", t=round(time.time() - t0, 1), state=snap(d) if s == 200 else cur)
            last = cur
    print("DONE")


if __name__ == "__main__":
    main()
