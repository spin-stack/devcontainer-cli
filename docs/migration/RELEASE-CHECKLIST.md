# Release checklist and parity declaration

This is the single exit checklist for declaring that the Go CLI is at parity
with the TypeScript oracle pinned in `reference/`. The narrative status lives in
[`GO-REWRITE-STATUS.md`](GO-REWRITE-STATUS.md).

Deliberate divergences are recorded in [`../DIVERGENCES.md`](../DIVERGENCES.md); this
checklist only decides whether a candidate commit can be released.

**As of the current candidate the automated gates below pass green** (parity-runtime
**189/0/0**, all lanes ✅ in CI). What remains is the release-identity and publish steps,
which are performed when the first tag is cut (that runs `release.yml` for real).

## Release identity

- [ ] `reference/` points to the expected target tag (currently v0.88.0).
- [ ] The exact submodule SHA is recorded in `reference-commit.txt`.
- [ ] The working tree used by CI corresponds to the candidate commit/tag.
- [ ] The binary reports the expected version and the cross-builds finish successfully.

## Baseline quality — ✅ green on the candidate

- [x] `task lint` passes.
- [x] `task coverage` passes and `coverage.out` is saved as an artifact.
- [x] `task test:integration` passes on a runner that allows local listeners.
- [x] `task test:e2e` passes with Docker and leaves no tagged containers behind.
- [x] There are no new untested regressions in CLI, OCI, lifecycle, or Docker/Compose.

## Observable parity — ✅ green on the candidate

- [x] `task parity:contract` finishes with no `failed` or `inconclusive`.
- [x] `task parity:network` finishes with no `failed` or `inconclusive`.
- [x] `task parity:runtime` finishes with no `failed` or `inconclusive` (189/0/0).
- [x] Every selected case finishes as `matched`; capability skips are
      explained and do not affect the mandatory lane.
- [x] The `deferred-runtime` cases were executed and their YAML status updated
      from evidence (promoted to `match` from the green runtime run).
- [x] Publishing of features and templates was compared against `registry:3`, including
      tags, manifests, and collection metadata.
- [x] `features test` ran at least one real A/B scenario and verified cleanup.

## Artifacts and decision

- [x] CI retained `parity-contract.json`, `parity-network.json`,
      `parity-runtime.json`, `reference-commit.txt`, and `coverage.out`.
- [x] The JSON files account for every case in the matrix across `matched`, `failed`,
      `skipped-docker`, `skipped-network`, `inconclusive`, and `not-selected`.
- [x] A `PASS` with omitted cases was not used as evidence of parity.
- [x] Deliberate divergences are documented ([`../DIVERGENCES.md`](../DIVERGENCES.md)).
- [ ] **(tag step)** Cut the release tag → `release.yml` builds/signs the artifacts and
      publishes the image; only then is the status changed to "full parity".

## Equivalent local commands

```sh
task lint
task coverage
task test:integration
task test:e2e
task reference
task parity:contract
task parity:network
task parity:runtime
task build:cross
```
