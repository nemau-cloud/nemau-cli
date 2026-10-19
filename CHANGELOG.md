# Changelog

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/) (0.x: a minor
release may change behaviour). The version is stamped from the git tag at
build time.

## [Unreleased]

### Added

- A user guide in `docs/` and a command reference in `docs/commands`, generated
  from the help texts with `go run ./internal/tools/gendocs`.
- `CONTRIBUTING.md` and `SECURITY.md`.

### Changed

- The README is shorter. The detailed material moved to the user guide.
- The main package moved to `cmd/nemau`, so `go install
  github.com/nemau-cloud/nemau-cli/cmd/nemau@latest` installs a binary named `nemau`.
- `nemau lb pool add-monitor` and `update-monitor` take `--probe-timeout` for
  the probe timeout. Their old `--timeout` flag hid the global `--timeout`,
  which bounds the whole command.
- Built on `nemau-sdk-go` v0.5.1.
- `nemau export` writes `source = "nemau-cloud/nemau"`, the provider's registry
  address.

### Fixed

- `nemau lb instance` no longer has the alias `ls`, which belongs to list
  commands.

## [0.5.0]

Built on `nemau-sdk-go` v0.5.0, which this release is versioned with.

### Added

- `--idempotency-key` on every write. Without it the CLI generates a key per
  command and keeps it across its own retries. Re-running a command with the
  same key within 24 hours returns the original task and prints
  `replayed: ...`; the same key with different parameters is refused
  (`422 idempotency_key_mismatch`) with a hint.
- Hints for `422` and `429` refusals.
- `nemau k8s cluster upgrade <name> --version <v>` runs the guided upgrade as a
  single task: preflight checks, maintenance window and rolling upgrade.

### Changed

- `nemau storage object list` follows the platform's page cursor (at most
  1000 objects per request) until the container is exhausted. `--limit` stops
  early and prints the cursor to continue with `--marker`.
- A `429` or `503` is retried with the `Retry-After` it names (up to 60 s; a
  longer wait is reported instead). Writes are only retried when they carry an
  idempotency key, which every asynchronous write does.
- `nemau compute instance metrics --period` accepts `1h`, `6h`, `24h` and `7d`
  only.
- Built with Go 1.27.2 for the standard library security fixes
  (GO-2026-6603 to GO-2026-6617).

### Notes

- Access keys expire: 90 days by default, 365 at most, five active keys per
  user. After the expiry the API answers `401`; create the next key before then.
