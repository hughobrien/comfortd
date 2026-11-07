"""Write redacted Kumo fixtures from spike/out into testdata/kumo. Usage: redact.py SRC DST"""
import json, re, sys
from pathlib import Path

src, dst = Path(sys.argv[1]), Path(sys.argv[2])
dst.mkdir(parents=True, exist_ok=True)

load = lambda p: json.loads((src / p).read_text())["data"]
sites = load("sites.json")
site_id = sites[0]["id"]
zones = load(f"zones_{site_id}.json")
serial_of = {z["name"]: z["adapter"]["deviceSerial"] for z in zones}

mapping, counts = {}, {}
def fake(kind, real):
    if real not in mapping:
        counts[kind] = n = counts.get(kind, 0) + 1
        mapping[real] = {
            "serial": f"TESTSERIAL{n:04d}",
            "uuid": f"00000000-0000-4000-8000-{n:012d}",
        }[kind]
    return mapping[real]

for name in ("Office", "House"):
    fake("serial", serial_of[name])

UUID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
SECRET_KEYS = {"mac": "00:00:00:00:00:00", "mak": "REDACTED", "baseMAK": None, "routerSsid": "REDACTED"}

def scrub(v, key=None):
    if isinstance(v, dict):
        return {k: scrub(x, k) for k, x in v.items()}
    if isinstance(v, list):
        return [scrub(x) for x in v]
    if key in SECRET_KEYS and v is not None:
        return SECRET_KEYS[key]
    if isinstance(v, str):
        if v in mapping:
            return mapping[v]
        if key in ("deviceSerial", "serialNumber"):
            return fake("serial", v)
        if UUID.match(v):
            return fake("uuid", v)
    if key == "name" and v == sites[0]["name"]:
        return "Home"
    return v

def out(name, obj):
    obj = scrub(obj)
    # also scrub dict keys that are uuids (none expected in these files)
    (dst / name).write_text(json.dumps(obj, indent=2, sort_keys=True) + "\n")

out("sites.json", sites)
out("zones.json", zones)
for name in ("Office", "House"):
    s = serial_of[name]
    low = name.lower()
    out(f"device_{low}.json", load(f"device_{s}.json"))
    out(f"profile_{low}.json", load(f"device_{s}_profile.json"))
    out(f"status_{low}.json", load(f"device_{s}_status.json"))
    out(f"sensor_{low}_404.json", load(f"device_{s}_sensor.json"))

# Login response shape only: real tokens and profile are never stored.
(dst / "login.json").write_text(json.dumps(
    {"token": {"access": "test-access-token", "refresh": "test-refresh-token"},
     "username": "user@example.com"}, indent=2) + "\n")
(dst / "refresh.json").write_text(json.dumps(
    {"access": "test-access-token-2", "refresh": "test-refresh-token-2"}, indent=2) + "\n")
print("wrote", sorted(p.name for p in dst.iterdir()))
