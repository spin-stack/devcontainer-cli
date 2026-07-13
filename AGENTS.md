# Repository guidelines

## Agent ground rules (read first)

- **Never create, amend, squash, or push commits unless the user explicitly requests it.** Preparing a change and creating its commit are separate steps; default to the first only.
- Keep changes focused on the requested behavior. Do not reformat, rename, clean up, or refactor unrelated code while implementing a fix or feature.
- Preserve pre-existing working-tree changes and never assume they belong to the current task.
- Generated files may be updated only when the requested change requires it, using the repository's documented generator.

## Orientation

- **Go version:** 1.26
- **Build:** `task build`
- **Lint:** `task lint`
- **Prepare the TypeScript reference oracle:** `task reference`
- **Hermetic reference comparison:** `task parity:contract`
- **Docker-backed reference comparison:** `PARITY_COMMAND=<command> task parity:runtime`
- **List all tasks:** `task --list`

## Reference compatibility

Treat observable compatibility with the reference Dev Container CLI as a product requirement. Before changing CLI output, defaults, lifecycle order, configuration merging, mounts, Feature behavior, or error behavior, run `task reference`, inspect the implementation under `reference/`, and review the existing parity coverage. Use `task parity:contract` for hermetic behavior and `PARITY_COMMAND=<command> task parity:runtime` when the comparison requires Docker.

Do not introduce an intentional divergence silently. Document the divergence, add a focused parity test, and explain why it is necessary. A behavior that looks surprising is not sufficient reason to diverge from the reference.

## Public contracts

CLI flags, exit codes, JSON output, stdout/stderr separation, configuration defaults, archive names, image tags, and release artifact names are public contracts. Changes to these contracts require:

- an explicit regression or contract test;
- a backward-compatibility analysis;
- documentation and migration guidance when users must take action;
- a breaking-change commit when compatibility cannot be preserved.

## Error handling

Do not convert unexpected failures into silent fallbacks, empty results, or successful exit codes. Preserve actionable context when wrapping errors and use errors that callers can inspect when behavior depends on the failure type.

A fallback is acceptable only when it is part of the documented contract and is covered by tests for both the primary and fallback paths. Do not weaken validation or error reporting merely to match a test expectation.

## Regression tests are mandatory

Every bug fix and every behavior change must include a regression test in the same change. The test must demonstrate the affected behavior and must be capable of failing before the implementation change and passing after it.

Choose the narrowest test level that matches the real failure mode:

- **unit test** for isolated parsing, validation, transformation, or helper behavior;
- **integration or contract test** when the change crosses packages, processes, filesystems, or command boundaries;
- **end-to-end or parity test** when the regression depends on Docker, the CLI's observable behavior, lifecycle execution, networking, or compatibility with the reference implementation;
- **workflow/packaging/smoke assertion** for CI and release changes where a Go test cannot exercise the behavior directly.

Do not weaken, delete, or broadly rewrite an existing assertion merely to make a change pass. Cover both the reported failure and important boundary cases when they are part of the same risk. A test that only exercises the new code without asserting the externally relevant result is not a regression test.

If the repository has no suitable test harness, add the smallest appropriate harness as part of the change. Do not mark a fix or behavior change complete while its regression test is missing. Pure documentation changes that do not alter executable examples or documented contracts are the only exception.

## Required validation

Fast checks — run for every change while iterating:

- affected package tests;
- `task lint`.

Area checks — run the ones matching every affected area before reporting completion (these are slower; do not run e2e/parity for changes that do not touch those boundaries):

- Go code: `task test:race`;
- configuration or specification behavior: `task spec:compliance`;
- CLI contracts or observable output: `task parity:contract`;
- network behavior: `task parity:network`;
- Docker, lifecycle, or container behavior: `task test:e2e` and the affected runtime parity coverage;
- Features or Templates publishing: `task parity:publish`;
- release or packaging changes: `goreleaser check` plus the applicable build or smoke test;
- dependency or security-sensitive changes: `task vuln`.

Do not substitute a narrower check when the change crosses one of the boundaries above.

Report the commands executed and their results in the final handoff. If a required check cannot run because of the environment, state why, what was validated instead, and what remains unverified. Never claim that a check passed when it was not executed.

## Commit messages

Only relevant when the user explicitly asks for a commit (see ground rules).
Before creating one, inspect the complete staged diff, run the relevant checks, and do not mix unrelated changes, user-owned work, or generated artifacts into the commit.

Use Conventional Commits. This format feeds the changelog in `.goreleaser.yml`, so the type must describe the user-visible effect:

```text
<type>(<optional-scope>): <imperative summary>
```

| Type | Changelog section |
| --- | --- |
| `feat` | Features |
| `fix` | Bug fixes |
| `perf` | Performance |
| `refactor`, `ci`, `build` | Internal improvements |
| `revert` | Other changes |
| `docs`, `test`, `chore` | excluded |

Subjects: imperative mood, lowercase after the colon, no trailing period, ≤72 characters, one logical change, describe the result not the process.
Use a short lowercase scope when it adds context (`config`, `docker`, `cli`, `release`, `parity`, `security`); never combine types or scopes with `+`.

For a breaking change, add `!` before the colon and explain the migration in the body, plus a `BREAKING CHANGE:` footer when more detail is useful:

```text
feat(config)!: reject deprecated mount syntax

BREAKING CHANGE: replace `old-option` with `new-option` in devcontainer.json.
```

```text
fix(docker): preserve build arguments during retry
docs: document rootless Docker requirements
```

Use a body when the reason, tradeoff, or non-obvious behavior matters; wrap at ~72 characters. Reference issues/PRs in footers (`Fixes #123`).
