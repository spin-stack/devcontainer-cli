# Release checklist and parity declaration

This is the single exit checklist for declaring that the Go CLI is at parity
with the TypeScript oracle pinned in `reference/`. The narrative status lives in
[`GO-REWRITE-STATUS.md`](GO-REWRITE-STATUS.md).

Pending implementation is tracked in
[`REMAINING-WORK.md`](REMAINING-WORK.md); this checklist only decides whether a
candidate commit can be released.

## Release identity

- [ ] `reference/` points to the expected target tag (currently v0.88.0).
- [ ] The exact submodule SHA is recorded in `reference-commit.txt`.
- [ ] The working tree used by CI corresponds to the candidate commit/tag.
- [ ] The binary reports the expected version and the cross-builds finish successfully.

## Baseline quality

- [ ] `task lint` passes.
- [ ] `task coverage` passes and `coverage.out` is saved as an artifact.
- [ ] `task test:integration` passes on a runner that allows local listeners.
- [ ] `task test:e2e` passes with Docker and leaves no tagged containers behind.
- [ ] There are no new untested regressions in CLI, OCI, lifecycle, or Docker/Compose.

## Observable parity

- [ ] `task parity:contract` finishes with no `failed` or `inconclusive`.
- [ ] `task parity:network` finishes with no `failed` or `inconclusive`.
- [ ] `task parity:runtime` finishes with no `failed` or `inconclusive`.
- [ ] Every selected case finishes as `matched`; capability skips are
      explained and do not affect the mandatory lane.
- [ ] The `deferred-runtime` cases were executed and their YAML status updated
      from evidence, not by anticipatory declarative editing.
- [ ] Publishing of features and templates was compared against `registry:3`, including
      tags, manifests, and collection metadata.
- [ ] `features test` ran at least one real A/B scenario and verified cleanup.

## Artifacts and decision

- [ ] CI retained `parity-contract.json`, `parity-network.json`,
      `parity-runtime.json`, `reference-commit.txt`, and `coverage.out`.
- [ ] The JSON files account for every case in the matrix across `matched`, `failed`,
      `skipped-docker`, `skipped-network`, `inconclusive`, and `not-selected`.
- [ ] A `PASS` with omitted cases was not used as evidence of parity.
- [ ] Deliberate divergences are documented in the current status.
- [ ] Only after all the preceding items are complete is the status changed to
      "full parity" and the release created.

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
