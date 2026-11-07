# comfortd — notes for agents

HomeKit bridge and web UI for two Mitsubishi Comfort (Kumo Cloud) air conditioners, running on bigthink (Hugh's NixOS server).

## Start here

1. `plan.md`: current state, the segment list (which one is next), how we work, and the Log. Read it first; update its Log when you finish something.
2. `docs/superpowers/specs/2026-11-07-comfortd-design.md`: the design. Pinned values (ports, paths, timings, JSON shapes, mappings) are decisions, not suggestions.
3. `docs/superpowers/plans/2026-11-07-s<N>-*.md`: the plan for the segment you're executing. Each task's Interfaces block names what earlier tasks produced.
4. `docs/api-inventory.md`: what we know about the Kumo API and how sure we are.

## Hard rules

- **No command reaches a real AC unless Hugh agreed to that specific change in this conversation**, beyond what a plan step explicitly marks as Hugh-approved. Tests use `pkg/kumo/kumotest` only. Writes are tested on Office before House.
- **Secrets:** the Comfort login lives in `./comfort` (gitignored, Mac) and `/persist/comfortd/credentials` (bigthink, root 0600). Never print, commit, or log them, or tokens. `spike/out/` is gitignored raw data with real serials. Only redacted copies (`pkg/kumo/kumotest/fixtures`, made by `spike/redact_fixtures.py`) go in git.
- **This repo is public.** No serials, MACs, account details or Hugh's address in commits.
- **bigthink deploys:** you are authorised to run `nixos-rebuild` for comfortd changes. Follow `plan.md` "How we work" exactly: edit `~/.config/nixos`, diff against `/etc/nixos`, `sudo cp`, rebuild. Update only the `comfortd` flake input. If the diff shows anything unrelated, stop and ask. Hugh's general bigthink notes live in `~/.claude/projects/-Users-hugh/memory/` (`bigthink-nixos-config-workflow.md`, `bigthink-sandbox-cold-boot.md`, `bigthink-deliberate-choices.md`); read them before touching the host.
- **Rebooting bigthink needs Hugh's OK** (it runs the house).
- **Library versions:** newest stable, except the two pins in the spec's Constraints: the `go` directive and the templ version both follow the flake's nixpkgs. `flake.lock` may be updated freely.

## Commands

- Mac: `brew install go` once. Then `go test ./...`, `go test -race ./...`, and `go run ./cmd/comfortd -fake -state-dir /tmp/comfortd-dev` (a full daemon against the in-process fake, UI at http://127.0.0.1:9879/).
- Templates: `go run github.com/a-h/templ/cmd/templ@v<pinned> generate`, then commit the `*_templ.go` files.
- Gate (nix exists only on bigthink): `just gate`, or the rsync + `nix build path:…#checks.x86_64-linux.gate` command in segment 1 Task 7. Check the printed `NIX EXIT=`.
- Live daemon: `ssh bigthink curl -s http://127.0.0.1:9879/v1/units`; `https://comfortd.wg/`; `journalctl -u comfortd`.

## Style

Follow `../breezyd` and `../keylight-hap` conventions: SPDX headers, stdlib first, small focused files, table tests, `slog`. Commit per task; push to `main` when the gate is green.
