# Segment 1: read-only daemon, deployed — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `comfortd` runs on bigthink, logs in to Kumo Cloud, polls both units every 60 s, and serves the latest raw API JSON at `http://127.0.0.1:9879/v1/...`. No writes, no HomeKit, no UI page.

**Architecture:** `pkg/kumo` (API client) and `pkg/kumo/kumotest` (fake server on redacted fixtures) feed `internal/poller`, which fills `internal/store`. `cmd/comfortd` wires flags and an `http.ServeMux`. Nix flake + NixOS module ship it; bigthink's config enables it.

**Tech Stack:** Go (stdlib `net/http`, `log/slog`, `encoding/json`), Nix `buildGoModule`, Forgejo Actions native runner.

**Spec:** `docs/superpowers/specs/2026-11-07-comfortd-design.md` (read "Units", "Polling", "Auth and the account", "Packaging and deploy" first).

## Global Constraints

- Module path `github.com/hughobrien/comfortd`; license GPL-3.0-or-later (copy `../keylight-hap/LICENSE`); every `.go` file starts with `// SPDX-License-Identifier: GPL-3.0-or-later`.
- Newest stable library versions, but the `go` directive must not exceed the Go version of **either** nixpkgs that builds it: comfortd's own lock (on bigthink, in the repo copy, `nix eval --raw --inputs-from . nixpkgs#go.version`) and bigthink's system nixpkgs (`ssh bigthink nix eval --raw nixpkgs#go.version`; 1.26.10 on 2026-11-07). bigthink's input uses `inputs.nixpkgs.follows`, like breezyd and keylight-hap, so the deployed build uses bigthink's nixpkgs. Use the lower version, as `go 1.26`-style or patch-level.
- `vendorHash`: `null` while there are no dependencies. Whenever `go.mod`/`go.sum` change, set it to `pkgs.lib.fakeHash`, run the gate build, and paste the `got:` hash. This applies to every later segment that adds a dependency.
- No dependencies outside the standard library in this segment.
- **Never send a command to a real AC.** This segment has no write path; `SendCommand` is exercised only against `kumotest`.
- Never log or serve: password, access/refresh tokens, the `Authorization` header, the `/login` response body.
- API base `https://app-prod.kumocloud.com/v3`; header `x-app-version: 3.2.4` on every request; bearer token on every request except `/login` and `/refresh`.
- Access token is refreshed when older than 15 min. Login auth-rejection blocks logins for 15 min; other login failures back off 30 s doubling to 15 min. 429 retries after 2, 4, 8 s.
- Defaults: `--listen 127.0.0.1:9879`, `--state-dir /var/lib/comfortd`, `--poll-interval 60s`, `--credentials $CREDENTIALS_DIRECTORY/credentials`. Each flag also reads env `COMFORTD_<FLAG_UPPER_SNAKE>` (keylight-hap's `envOr` pattern).
- Files written to the state dir are written atomically (temp file + rename) with mode 0600.
- Local loop: `brew install go` on the Mac, `go test ./...`. Nix runs only on bigthink (see Task 7).

## Review Focus

1. **Restart loop must not mean login loop.** Restarting the daemon with a valid `tokens.json` performs zero logins. Pinned in Task 3 (`TestStoredTokenReused`) and checked live in Task 8.
2. **Wrong password.** A rejected login must not be retried before 15 min, whatever the poll interval. Pinned in Task 3 (`TestLoginBackoffAfterAuthReject`).
3. **Cloud down at startup with a cache.** Discovery falls back to `discovery.json` and the daemon still serves. Pinned in Task 5 (`TestDiscoverFallsBackToCache`).
4. **Numbers that arrive as ints.** `roomTemp` is `20` in one fixture and `20.5` in another; parsing must accept both. Pinned in Task 4 (`TestParseDeviceIntAndFloat`).
5. **Token rotation on refresh, and an unverified refresh body shape.** `/refresh` returns a new refresh token; the old one must not be reused, and the new pair must be persisted. Both the flat and the nested body are accepted. Pinned in Task 3 (`TestRefreshRotatesAndPersists`, `TestRefreshNestedShape`) and checked live in Task 8 Step 6.

---

### Task 1: Repository scaffold and gate

**Files:**
- Create: `go.mod`, `cmd/comfortd/main.go`, `cmd/comfortd/main_test.go`, `flake.nix`, `justfile`, `.forgejo/workflows/test.yml`, `LICENSE`
- Modify: `.gitignore` (add `/comfortd`, `/result`, `coverage.out`)

**Interfaces:**
- Produces: `var version = "dev"` in `cmd/comfortd/main.go`; `-version` flag prints it and exits 0.

- [ ] **Step 1:** `brew install go` (if `go version` fails). `go mod init github.com/hughobrien/comfortd`, then edit the `go` line down to bigthink's system Go (`ssh bigthink nix eval --raw nixpkgs#go.version`). Re-check against comfortd's own lock in Task 7.
- [ ] **Step 2: Write the failing test** `TestVersionFlag` in `cmd/comfortd/main_test.go`: runs `run([]string{"-version"}, &stdout)` and asserts exit code 0 and stdout `"dev\n"`.
- [ ] **Step 3:** Run `go test ./cmd/comfortd/` → FAIL (undefined `run`).
- [ ] **Step 4:** Implement `func run(args []string, stdout io.Writer) int` using a `flag.NewFlagSet`; `main` calls `os.Exit(run(os.Args[1:], os.Stdout))`. Keeping logic in `run` lets later tasks test flag wiring.
- [ ] **Step 5:** Run `go test ./...` → PASS.
- [ ] **Step 6:** Write `flake.nix` modelled on `../keylight-hap/flake.nix`:
  - `pname = "comfortd"`, `subPackages = [ "cmd/comfortd" ]`, `ldflags = [ "-s" "-w" "-X main.version=${version}" ]`.
  - Since there are no deps, `vendorHash = null`.
  - devShell packages: `go gopls gotools just`.
  - `checks.gate` checkPhase: `go vet ./...`, the gofmt drift check, `go test ./...`, `CGO_ENABLED=1 CC=clang go test -race ./...`.
  - `nixosModules.default` imports `./nix/module.nix` (created in Task 7; until then leave the `nixosModules` line out).
- [ ] **Step 7:** Copy `../keylight-hap/.forgejo/workflows/test.yml` verbatim. Write `justfile` with recipes `test` (`go test ./...`), `race`, `build` (`go build -o comfortd ./cmd/comfortd`), `gate` (the rsync + remote `nix build` command from Task 7 Step 4).
- [ ] **Step 8:** Commit: `git add -A && git commit -m "scaffold: go module, flake, gate, CI"`. Don't push yet (the gate is first proven in Task 7).

### Task 2: Fake Kumo server

**Files:**
- Create: `pkg/kumo/kumotest/server.go`, `pkg/kumo/kumotest/server_test.go`
- Existing: `pkg/kumo/kumotest/fixtures/*.json` (redacted real responses; regenerate only with `spike/redact_fixtures.py`). Office is `TESTSERIAL0001`, House is `TESTSERIAL0002`, the site id is `00000000-0000-4000-8000-000000000001`.

**Interfaces:**
- Produces (package `kumotest`):
  - `const User = "user@example.com"`, `const Password = "test-password"`, `const OfficeSerial = "TESTSERIAL0001"`, `const HouseSerial = "TESTSERIAL0002"`, `const SiteID = "00000000-0000-4000-8000-000000000001"`
  - `func New() *Server` (starts an `httptest.Server`; `s.URL` is the base, equivalent to `.../v3`)
  - `func (s *Server) Close()`
  - `func (s *Server) Logins() int`, `func (s *Server) Refreshes() int`
  - `type Command struct { Serial string; Commands map[string]any }`; `func (s *Server) Commands() []Command`
  - `func (s *Server) FailNext(path string, status, n int)` (the next n requests whose URL path equals `path` get `status` with body `{}`)
  - `func (s *Server) ExpireAccess()` (every issued access token now gets 401)
  - `func (s *Server) SetField(serial, field string, v any)` (simulates the app or a schedule changing the unit)
  - `func (s *Server) Device(serial string) map[string]any`
  - `func (s *Server) Requests(path string) int`
  - `func (s *Server) SetRefreshNested(nested bool)`

Behaviour to implement (all bodies JSON):

| Route | Behaviour |
|---|---|
| `POST /login` | body `{username,password,appVersion}`; wrong creds → 403; else 200 with `fixtures/login.json`'s shape and new tokens `access-<n>`, `refresh-<n>` |
| `POST /refresh` | body `{refresh}`; must equal the latest issued refresh token, else 401; 200 `{access, refresh}` with new values (rotation). With `SetRefreshNested(true)`, the body is `{"token":{access, refresh}}` instead. The real shape is unverified (comfort_HA says flat), so the client must accept both |
| any other route | 400 if `x-app-version` is missing; 401 unless `Authorization: Bearer <current access>` |
| `GET /sites/` | `fixtures/sites.json` |
| `GET /sites/{SiteID}/zones` | `fixtures/zones.json`, with each zone's `adapter` fields `operationMode`, `power`, `spHeat`, `spCool`, `roomTemp` overlaid from the live device |
| `GET /devices/{serial}` | live device map (initialised from `device_office.json` / `device_house.json`) |
| `GET /devices/{serial}/profile`, `/status` | fixture |
| `GET /devices/{serial}/sensor` | 404 with `fixtures/sensor_*_404.json` |
| `POST /devices/send-command` | body `{deviceSerial, commands}`; unknown serial → 404; record the command; apply each field to the live device; `operationMode: "off"` also sets `power` 0; any other `operationMode` sets `power` 1; 200 `{"devices":[serial]}` |

- [ ] **Step 1: Write the failing tests** in `server_test.go`:
  - `TestLoginIssuesTokens`: POST `/login` with `User`/`Password` → 200, `token.access` non-empty; wrong password → 403; `Logins()` == 2.
  - `TestAuthRequired`: GET `/sites/` without bearer → 401; with the issued token but no `x-app-version` → 400; with both → 200 and the body decodes to a 1-element array.
  - `TestRefreshRotates`: refresh with the login's refresh token → 200 and a different `refresh`; reusing the old refresh token → 401.
  - `TestSendCommandOffSetsPowerZero`: send `{"operationMode":"off"}` to `OfficeSerial` → `Device(OfficeSerial)["power"]` == 0 (as float64 0 or int 0; compare via `fmt.Sprint` == "0"); then `{"operationMode":"heat","spHeat":21}` → power "1", spHeat "21"; `Commands()` has 2 entries.
  - `TestSensor404`: GET `/devices/TESTSERIAL0001/sensor` → 404.
  - `TestFailNext`: `FailNext("/devices/TESTSERIAL0001", 429, 1)` → first GET 429, second 200.
- [ ] **Step 2:** `go test ./pkg/kumo/kumotest/` → FAIL.
- [ ] **Step 3:** Implement. Embed fixtures with `//go:embed fixtures/*.json`. Use `http.ServeMux` patterns (`GET /devices/{serial}`), a mutex around all state, and decode device fixtures into `map[string]any`.
- [ ] **Step 4:** `go test ./pkg/kumo/kumotest/` → PASS.
- [ ] **Step 5:** Commit `kumotest: fake Kumo server on redacted fixtures`.

### Task 3: Kumo client auth and token persistence

**Files:**
- Create: `pkg/kumo/client.go`, `pkg/kumo/auth.go`, `pkg/kumo/creds.go`, `pkg/kumo/auth_test.go`

**Interfaces:**
- Consumes: `kumotest` (Task 2).
- Produces (package `kumo`):
  - ```go
    type Options struct {
        BaseURL         string        // default DefaultBaseURL
        AppVersion      string        // default DefaultAppVersion
        CredentialsPath string        // required
        TokenPath       string        // "" = don't persist
        HTTP            *http.Client  // nil = &http.Client{Timeout: 15 * time.Second}
        Now             func() time.Time                              // nil = time.Now
        Sleep           func(ctx context.Context, d time.Duration) error // nil = real sleep honouring ctx
    }
    const DefaultBaseURL = "https://app-prod.kumocloud.com/v3"
    const DefaultAppVersion = "3.2.4"
    func New(o Options) *Client
    func (c *Client) LoginCount() int
    func (c *Client) RefreshCount() int
    func ReadCredentials(path string) (user, pass string, err error)
    var ErrAuth = errors.New("kumo: credentials rejected")
    var ErrLoginBackoff = errors.New("kumo: login backing off")
    ```
  - Tokens file JSON: `{"access":"…","refresh":"…","obtainedAt":"RFC3339"}`.
  - Internal `func (c *Client) token(ctx context.Context, forceRefresh bool) (string, error)`, used by Task 4's requests. It implements the startup order (stored access token if younger than 15 min, else refresh, else login), refresh-on-age and login backoff. With `forceRefresh` true, it skips the stored access token and refreshes (falling back to login). Tests below call `c.token(ctx, false)`.

- [ ] **Step 1: Write the failing tests** (each uses `kumotest.New()`, a temp creds file containing `User\nPassword\n`, and an injected `Now` you advance by hand):
  - `TestReadCredentials`: `"a@b\r\npw \n"` → `("a@b","pw")`; a single line → error.
  - `TestLoginOnFirstUse`: `c.token(ctx)` → non-empty; `srv.Logins()` == 1; `tokens.json` exists with mode 0600.
  - `TestStoredTokenReused`: client A gets a token; client B is created on the same `TokenPath` with `Now` = +5 min; B's `token()` causes `srv.Logins()` to stay 1 and `srv.Refreshes()` 0.
  - `TestRefreshRotatesAndPersists`: get a token, advance `Now` 16 min, `token()` again → `srv.Refreshes()` == 1, `Logins()` == 1, and `tokens.json`'s `refresh` equals the server's newest refresh token (a second refresh at +32 min succeeds, which proves the rotated token was kept).
  - `TestRefreshNestedShape`: `srv.SetRefreshNested(true)`, stale token → refresh succeeds (`Refreshes()` == 1, `Logins()` == 1, not 2).
  - `TestRefreshFailureFallsBackToLogin`: stale stored token, server-side refresh token invalid (start a fresh `kumotest` so stored tokens are unknown) → `token()` succeeds with `Logins()` == 1.
  - `TestLoginBackoffAfterAuthReject`: creds file has the wrong password → first `token()` returns an error wrapping `ErrAuth`; a second call at +1 min returns `ErrLoginBackoff` and `srv.Logins()` is still 1; at +16 min it tries again (`Logins()` == 2).
- [ ] **Step 2:** `go test ./pkg/kumo/` → FAIL.
- [ ] **Step 3:** Implement. Guard token state with a mutex: concurrent callers share one login/refresh. Re-read credentials on each login. Log `slog.Info("kumo login", "count", n)` and `slog.Info("kumo refresh", "count", n)` on success. On failure, log `slog.Warn("kumo login failed", "err", err)` / `"kumo refresh failed"`; the error text must not contain the body. A refresh that falls back to login is logged as both lines, so the journal shows it.
- [ ] **Step 4:** `go test ./pkg/kumo/` → PASS.
- [ ] **Step 5:** Commit `kumo: auth, token persistence, login backoff`.

### Task 4: Kumo client requests and parsing

**Files:**
- Create: `pkg/kumo/request.go`, `pkg/kumo/types.go`, `pkg/kumo/request_test.go`, `pkg/kumo/types_test.go`

**Interfaces:**
- Produces:
  - ```go
    func (c *Client) Get(ctx context.Context, path string) (json.RawMessage, error) // path e.g. "/sites/"
    func (c *Client) SendCommand(ctx context.Context, serial string, commands map[string]any) error
    var ErrNotFound = errors.New("kumo: not found")
    type HTTPError struct{ Status int; Path string } // Error(): "kumo: GET /x: status 500"
    type Site struct{ ID, Name string }
    type Zone struct{ ID, Name, Serial string; HasActiveSchedule, HoldEnabled bool } // Serial = adapter.deviceSerial, HoldEnabled = holdMode.enabled
    type Device struct {
        Serial, ModelNumber, OperationMode, FanSpeed, AirDirection, UpdatedAt string
        Power int
        SpHeat, SpCool, RoomTemp, Humidity *float64
        Connected bool
        RealValues map[string]any
    }
    type Profile struct {
        Min, Max map[string]float64 // minimumSetPoints / maximumSetPoints: keys heat, cool, auto
        NumberOfFanSpeeds int
        HasModeDry, HasModeHeat, HasModeVent, HasVaneDir, HasVaneSwing bool
    }
    func (p Profile) SupportsCool() bool // "cool" key present in Max
    func (p Profile) SupportsAuto() bool
    func ParseSites(raw json.RawMessage) ([]Site, error)
    func ParseZones(raw json.RawMessage) ([]Zone, error)
    func ParseDevice(raw json.RawMessage) (Device, error)  // Serial = deviceSerial
    func ParseProfile(raw json.RawMessage) (Profile, error) // response is a 1-element array
    ```
  - Retry rule: on 401, call `c.token(ctx, true)`, retry once. On 429, sleep 2, 4, 8 s via `Options.Sleep`, then return `*HTTPError{429}`. On 404, return `ErrNotFound`.

- [ ] **Step 1: Write the failing tests:**
  - `TestGetSites`: `Get(ctx,"/sites/")` → `ParseSites` gives 1 site, ID `kumotest.SiteID`, name `"Home"`.
  - `TestParseZonesFixture`: zones → 2 zones, names `{"House","Office"}`, Office's `Serial` == `kumotest.OfficeSerial`, `HasActiveSchedule` true.
  - `TestParseDeviceIntAndFloat`: office fixture → `*RoomTemp` == 20.5, `*SpHeat` == 19, `RealValues["spHeat"]` present, `Humidity` nil; house fixture → `*RoomTemp` == 20, `*Humidity` == 52.
  - `TestParseProfileFixture`: office `Min["heat"]` == 10, `Max["heat"]` == 31, `NumberOfFanSpeeds` == 5, `SupportsCool()` true; house `Max["heat"]` == 28.
  - `TestGet404`: sensor path → `errors.Is(err, ErrNotFound)`.
  - `TestGet401RefreshesAndRetries`: get once, `srv.ExpireAccess()`, `Get` again succeeds; `srv.Refreshes()` == 1.
  - `TestGet429Backoff`: `FailNext(path, 429, 2)` → succeeds; injected `Sleep` saw `[2s, 4s]`. With `FailNext(path, 429, 4)` → `*HTTPError` status 429 after sleeps `[2s,4s,8s]`.
  - `TestSendCommandFake`: `SendCommand(ctx, OfficeSerial, {"spHeat":21})` → nil; `srv.Commands()[0]` matches. (Fake only. Never point this at the real API in tests.)
- [ ] **Step 2:** `go test ./pkg/kumo/` → FAIL.
- [ ] **Step 3:** Implement. Decode numbers with `json.Number` or `*float64` so both `20` and `20.5` work. Set `x-app-version` and `Content-Type: application/json` on every request.
- [ ] **Step 4:** `go test ./pkg/kumo/` → PASS.
- [ ] **Step 5:** Commit `kumo: GET, send-command, parsing, retry`.

### Task 5: Store, discovery and poller

**Files:**
- Create: `internal/store/store.go`, `internal/store/store_test.go`, `internal/poller/discover.go`, `internal/poller/poller.go`, `internal/poller/poller_test.go`, `internal/fsutil/atomic.go` (atomic 0600 write helper, shared with `pkg/kumo` token writes: move Task 3's helper here if you wrote one)

**Interfaces:**
- Consumes: `kumo.Client.Get`, `kumo.Parse*` (Task 4).
- Produces:
  - ```go
    package store
    const (EndpointZone = "zone"; EndpointDevice = "device"; EndpointProfile = "profile"; EndpointStatus = "status"; EndpointSensor = "sensor")
    type Unit struct{ Slug, Name, Serial, ZoneID, SiteID string }
    func Slug(name string) string // "Office" → "office", "Living Room 2" → "living-room-2"
    type Snapshot struct {
        Unit         Unit
        Raw          map[string]json.RawMessage // by endpoint
        At           map[string]time.Time
        Errors       map[string]string          // last error per endpoint, cleared on success
        Device       *kumo.Device
        Profile      *kumo.Profile
        Zone         *kumo.Zone
        LastDeviceOK time.Time
    }
    func (s Snapshot) Stale(now time.Time, interval time.Duration) bool // LastDeviceOK older than 3*interval, or Device != nil && !Device.Connected
    type Update struct{ Slug, Endpoint string; Old, New json.RawMessage; At time.Time } // Old nil on first sight
    func New(units []Unit) *Store
    func (s *Store) Units() []Unit                       // sorted by slug
    func (s *Store) Put(slug, endpoint string, raw json.RawMessage, at time.Time) error // parses device/profile/zone; calls subscribers with an Update
    func (s *Store) PutError(slug, endpoint string, err error, at time.Time)
    func (s *Store) Get(slug string) (Snapshot, bool)    // deep copy
    func (s *Store) All() []Snapshot                     // sorted by slug
    func (s *Store) Subscribe(fn func(Update))           // called synchronously after each Put, outside the lock, in registration order
    ```
  - ```go
    package poller
    type API interface{ Get(ctx context.Context, path string) (json.RawMessage, error) }
    func Discover(ctx context.Context, api API, cachePath string) ([]store.Unit, error) // live → write cache; live fails → read cache; both fail → error
    type Poller struct{ … }
    func New(api API, st *store.Store, interval time.Duration) *Poller
    func (p *Poller) PollOnce(ctx context.Context)  // zones (once, split per unit by zone ID) + device per unit
    func (p *Poller) PollSlow(ctx context.Context)  // profile + status per unit
    func (p *Poller) PollSensor(ctx context.Context) // sensor per unit; ErrNotFound → PutError(..., "not found")
    func (p *Poller) Run(ctx context.Context) error // PollSensor+PollSlow+PollOnce at start, PollOnce every interval, PollSlow every 6h, Kick handling
    func (p *Poller) Kick(slug string)              // extra device polls for slug at +3s, +8s, +15s (non-blocking)
    ```

- [ ] **Step 1: Write the failing tests:**
  - `TestSlug`: `"Office"`→`"office"`, `"Living Room 2"`→`"living-room-2"`, `" House! "`→`"house"`.
  - `TestPutParsesAndNotifies`: subscribe, `Put("office", EndpointDevice, officeFixture, t0)` → snapshot `Device.OperationMode` == "heat"; the Update has `Old == nil`; a second Put has `Old` equal to the first raw.
  - `TestSubscribersInOrder`: three subscribers append 1, 2, 3 to a slice → after a Put the slice is `[1 2 3]`.
  - `TestStale`: `LastDeviceOK` = now−2.5×interval → not stale; now−3.5×interval → stale; device `connected:false` → stale.
  - `TestDiscoverLiveWritesCache`: against `kumotest` → 2 units `{house, office}` with serials; cache file exists.
  - `TestDiscoverFallsBackToCache`: run live once (writes cache), then `Discover` with an API whose `Get` always errors → same 2 units; with no cache → error.
  - `TestPollOnceFillsStore`: after `PollOnce`, both snapshots have `Raw[EndpointDevice]` and `Raw[EndpointZone]`, and the zone raw for office has `"name":"Office"`. `srv.Requests("/sites/"+kumotest.SiteID+"/zones")` == 1.
  - `TestPollSeesExternalChange`: `srv.SetField(OfficeSerial,"spHeat",23)`, `PollOnce` → office `*Device.SpHeat` == 23.
  - `TestPollErrorRecorded`: `FailNext(device path, 500, 1)`, `PollOnce` → `Errors["device"]` non-empty and earlier raw still present; next `PollOnce` clears it.
  - `TestKickPollsSoon`: use a short interval and a fake clock or real 3 s wait (mark with `testing.Short()` skip if real time is used) → `srv.Requests(device path)` increases by 3 after a Kick.
- [ ] **Step 2:** `go test ./internal/...` → FAIL.
- [ ] **Step 3:** Implement. Zone split: decode the zones array into `[]json.RawMessage`, match by each element's `id` against `Unit.ZoneID`.
- [ ] **Step 4:** `go test -race ./internal/...` → PASS.
- [ ] **Step 5:** Commit `store, discovery, poller`.

### Task 6: Daemon wiring and JSON API

**Files:**
- Create: `cmd/comfortd/server.go`, `cmd/comfortd/server_test.go`
- Modify: `cmd/comfortd/main.go`, `cmd/comfortd/main_test.go`

**Interfaces:**
- Consumes: Tasks 3–5.
- Produces:
  - `type deps struct{ Store *store.Store; Interval time.Duration; Now func() time.Time }` and `func newMux(d deps) *http.ServeMux`. Later segments add fields to `deps` and routes to `newMux`.
  - Routes:
    - `GET /healthz`: 200 body `ok\n`, or 503 body `stale: office,house\n`.
    - `GET /v1/units`: `[{"slug","name","serial","stale","lastDeviceOK","errors"}]`.
    - `GET /v1/units/{slug}/raw/{endpoint}`: raw bytes, `Content-Type: application/json`. Unknown slug or endpoint → 404; endpoint never fetched → 404 `{"error":"not fetched"}`.
  - Flags: `-listen`, `-state-dir`, `-credentials`, `-api-base`, `-app-version`, `-poll-interval`, `-fake`, `-version`. `-fake` starts `kumotest.New()` in-process, writes `User`/`Password` to `<state-dir>/fake-credentials`, and points `-api-base` and `-credentials` at them: local dev on the Mac with no real account.
  - Files in the state dir: `tokens.json`, `discovery.json`.

- [ ] **Step 1: Write the failing tests:**
  - `TestUnitsEndpoint`: store with both units after a `PollOnce` against kumotest → `/v1/units` returns 2 entries, `stale` false.
  - `TestRawEndpoint`: `/v1/units/office/raw/device` → body JSON-equal to `srv.Device(OfficeSerial)`; `/v1/units/nope/raw/device` → 404; `/v1/units/office/raw/bogus` → 404.
  - `TestHealthz`: fresh → 200 `ok`; `Now` advanced 4×interval → 503 containing `office`.
  - `TestRunFakeServes`: `runContext(ctx, []string{"-fake","-listen","127.0.0.1:0","-state-dir",tmp}, io.Discard, ready)` in a goroutine, where `func runContext(ctx context.Context, args []string, stdout io.Writer, ready func(addr net.Addr)) int` calls `ready` with the bound listener address once serving (`run` calls it with `nil` ready). Poll `http://<addr>/v1/units` until 2 units; cancel; exit code 0. The daemon also logs `slog.Info("listening", "addr", …)`.
  - `TestNoSecretsInLogs`: capture `slog` output during `TestRunFakeServes`-style startup; assert it contains neither `kumotest.Password` nor `access-` nor `refresh-` nor `Bearer`.
- [ ] **Step 2:** `go test ./cmd/comfortd/` → FAIL.
- [ ] **Step 3:** Implement. Order in `runContext`:
  1. Parse flags and create the state dir.
  2. Build the client.
  3. Run `Discover`, retrying every 30 s and logging each failure, until it succeeds or ctx ends.
  4. Build the store and poller.
  5. Start the poller goroutine.
  6. `http.Server{ReadHeaderTimeout: 10 * time.Second}` serving until ctx is done, then `Shutdown`.
  Use `signal.NotifyContext` in `main` only.
- [ ] **Step 4:** `go test -race ./...` → PASS. Also run by hand: `go run ./cmd/comfortd -fake -listen 127.0.0.1:9879 -state-dir /tmp/comfortd-dev` and `curl -s localhost:9879/v1/units`.
- [ ] **Step 5:** Commit `comfortd: daemon wiring, /v1 JSON, -fake mode`.

### Task 7: NixOS module and a green gate

**Files:**
- Create: `nix/module.nix`
- Modify: `flake.nix` (add `nixosModules.default`/`nixosModules.comfortd` like keylight-hap)

**Interfaces:**
- Produces: options under `services.comfortd`:
  - `enable`, `package` (mkDefault from the flake as keylight-hap does)
  - `listen` (str, `"127.0.0.1:9879"`)
  - `credentialsFile` (path, no default; assertion if unset when enabled)
  - `pollInterval` (str, `"60s"`)
  - `homekit.enable` (bool, false), `homekit.port` (port, 0), `homekit.units` (listOf str, `[]`)
  - `writableUnits` (listOf str, `[]`)
  - `openFirewall` (bool, false)
  - `extraArgs` (listOf str, `[]`)

  The homekit and writable options are declared now but passed to the binary only from segments 4/5. Until then, the assertion `!cfg.homekit.enable && cfg.writableUnits == []` fails with the message "comfortd: homekit/writableUnits not implemented yet".

- [ ] **Step 1:** Write `nix/module.nix` from `../keylight-hap/nix/module.nix`:
  - systemd service `comfortd`, `DynamicUser = false`, its own system user/group `comfortd`, `StateDirectory = "comfortd"`.
  - `LoadCredential = [ "credentials:${cfg.credentialsFile}" ]`.
  - ExecStart passes `--listen`, `--state-dir /var/lib/comfortd`, `--credentials %d/credentials` and `--poll-interval`.
  - `after`/`wants` `network-online.target`; `Restart = "on-failure"`; `RestartSec = "10s"`.
  - Hardening block copied verbatim (keep `AF_NETLINK`).
- [ ] **Step 2:** Sync to bigthink and build the gate there (Mac has no nix):
  ```bash
  rsync -a --delete --exclude .git --exclude spike/out --exclude comfort ./ bigthink:~/comfortd-gate/
  ssh bigthink 'mkdir -p /tmp/cgate && cd ~/comfortd-gate && TMPDIR=/tmp/cgate nix build --no-write-lock-file -L "path:$PWD#checks.x86_64-linux.gate" > /tmp/cgate.log 2>&1; echo "NIX EXIT=$?"; tail -30 /tmp/cgate.log'
  ```
  Expected: `NIX EXIT=0`. Never pipe the build through `tail` directly; it hides the exit code.
- [ ] **Step 3:** Run `nix flake lock` in the same copy if `flake.lock` doesn't exist yet, copy `flake.lock` back to the Mac (`scp bigthink:~/comfortd-gate/flake.lock .`), commit it. Check the `go` directive against `nix eval --raw --inputs-from . nixpkgs#go.version` and lower it if needed.
- [ ] **Step 4:** Add `checks.module` (x86_64-linux only) that really evaluates the module: `(nixpkgs.lib.nixosSystem { system = "x86_64-linux"; modules = [ self.nixosModules.default { boot.isContainer = true; system.stateVersion = "26.05"; services.comfortd = { enable = true; credentialsFile = "/run/keys/comfortd"; }; } ]; }).config.systemd.units."comfortd.service".unit`. Build it on bigthink: `nix build --no-link -L "path:$PWD#checks.x86_64-linux.module"` and expect exit 0. Then `cat` the unit (`nix build --print-out-paths …`) and confirm `ExecStart` has `--credentials %d/credentials` and `LoadCredential=credentials:/run/keys/comfortd`. Also add a negative case: with `homekit.enable = true`, evaluation must fail with the not-implemented assertion (check by hand once; it doesn't need to be a flake check).
- [ ] **Step 5:** Commit `nix: module, flake.lock`. Push `main`. Watch the Forgejo run go green (`tea actions` or the API, see the `nix-ci-test-gates` skill). A red gate blocks Task 8.

### Task 8: Deploy to bigthink

Authorised: the agent may rebuild bigthink for comfortd (Hugh, 2026-11-07). Follow `plan.md` "How we work" exactly. Read `~/.claude/projects/-Users-hugh/memory/bigthink-nixos-config-workflow.md` first.

**Files (on bigthink, in `~/.config/nixos`):**
- Modify: `flake.nix` (input `comfortd = { url = "git+https://forgejo.wg/hughobrien/comfortd"; inputs.nixpkgs.follows = "nixpkgs"; };`, add it to the `outputs` args and `comfortd.nixosModules.default` to `modules`).
- Modify: `net-data.nix` (`ports.tcp.comfortd = 9879;` and `comfortdHomeKit = 9880;`, alphabetical like the rest).
- Modify: `storage.nix` (`"comfortd"` in `libServices`).
- Create: `services/comfortd.nix`:
  ```nix
  { net, net-lib, ... }:
  {
    services.comfortd = {
      enable = true;
      credentialsFile = "/persist/comfortd/credentials";
    };
    services.nginx.virtualHosts."comfortd.wg" = net-lib.mkHostProxyPass "comfortd" net.lo.ipv4;
  }
  ```
  Add `./comfortd.nix` to the `imports` list in `services/default.nix` (alphabetical; it currently lists `./asterisk.nix` … `./ups.nix`). Verified facts (2026-11-07): `net-lib.mkHostProxyPass host target` proxies to `http://<target>:${net.ports.tcp.<host>}` when `target == net.lo.ipv4`; `libServices` entries become impermanence-persisted `/var/lib/<name>` directories (keylight-hap uses the same with its own system user and `StateDirectory`, and works); the configuration is `nixosConfigurations.bigthink`.
- Not yet: the `network.nix` firewall entry (segment 4).

- [ ] **Step 1:** On bigthink run `cd ~/.config/nixos && git status --short`. It must be clean (if not, stop and ask Hugh). Make the edits above. Run `nix flake lock --update-input comfortd` (or `nix flake update comfortd`) so only this input moves.
- [ ] **Step 2:** `nix build .#nixosConfigurations.bigthink.config.system.build.toplevel --no-link` → succeeds.
- [ ] **Step 3:** `sudo diff -r -x .git -x .claude -x .forgejo ~/.config/nixos /etc/nixos`. The only differences must be your edits. Anything else: port it back or stop and ask.
- [ ] **Step 4:** Commit in `~/.config/nixos` (`comfortd: enable read-only daemon`) and push if it has a remote. `sudo cp` each changed or new file into `/etc/nixos/` (new directory entries: `sudo rsync` per the memory note, then `sudo chown -R root:root /etc/nixos`).
- [ ] **Step 5:** `doas nixos-rebuild switch` (or `sudo`). Expect `comfortd.service` started.
- [ ] **Step 6: Verify live:**
  - `systemctl is-active comfortd` → `active`.
  - `curl -s http://127.0.0.1:9879/v1/units` → 2 units, `stale: false` (within about 2 min).
  - `curl -s https://comfortd.wg/v1/units/office/raw/device` from the Mac (or via bigthink) → JSON with `operationMode`.
  - `journalctl -u comfortd --since -10min | grep -c 'kumo login'` → 1.
  - `sudo systemctl restart comfortd`, wait 70 s, then the same grep → still 1 per process start; the new process logs no `kumo login` (it reused `tokens.json`).
  - `journalctl -u comfortd | grep -i -E 'password|Bearer|refresh-|"access"'` → no output.
  - `sudo ls -la /var/lib/comfortd` → `tokens.json` and `discovery.json`, mode 0600.
  - **Refresh path (wait 20 min after the restart):** `journalctl -u comfortd --since -25min | grep -E 'kumo (login|refresh)'` → at least one `kumo refresh` and no new `kumo login`. If refresh fails and falls back to login every 15 min, that is the account-lockout risk: stop the service (`sudo systemctl stop comfortd`), look at the real `/refresh` status code and body *shape* (keys only, never values) via a temporary debug log or `spike/`-style script, fix the client and fake to match, and redeploy.
- [ ] **Step 7:** Ask Hugh whether the Comfort app on his phone is still logged in (does our login end the app's session?). Record the answer in `plan.md` Open questions.
- [ ] **Step 8:** Update `plan.md`: tick segment 1, add a dated Log line with what was verified and anything surprising (token lifetime seen, any 429s). Commit and push.

Reboot-survival is checked in segment 4 (it needs Hugh's OK, since a reboot interrupts the house).
