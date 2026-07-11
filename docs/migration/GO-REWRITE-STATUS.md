# Estado de paridad — Go CLI vs devcontainers/cli 0.88

Estado **actual** de la paridad del CLI Go contra el oráculo TypeScript (submódulo
`reference/`, v0.88.0). Este documento resume dónde estamos; el detalle pendiente
vive en el backlog y la historia en `git log`.

**Estado de release:** candidato con **corrida limpia lograda** contra el pin v0.88.0
(oracle `f683c29`): parity contract 68/0/0, network 13/0/0, runtime **189 matched / 0
failed / 0 inconclusive** + TestPublishParity, más lint/coverage/integration/e2e/build:cross;
artefactos JSON por lane + coverage.out + reference-commit.txt guardados. Falta
formalizarla en CI con `goreleaser`/`syft` instalados (RW-015/016) para declarar paridad
completa; ver [RELEASE-CHECKLIST.md](RELEASE-CHECKLIST.md).

**arm64 runtime: experimental, sin soporte por ahora.** Los binarios cross-compilan a
`linux/arm64`, pero correr contenedores arm64 (vía emulación QEMU/binfmt en hosts amd64)
es experimental: los casos de paridad `arm64_required` se saltan por default
(`skipped-arm64`, no afectan el gate) y sólo corren con `PARITY_ARM64=true`.

**Alcance soportado (firme):** **sólo Linux** (amd64/arm64) y **sólo Docker**. Windows y
macOS no son objetivos (sin runtime/E2E/release). **Podman no soportado**; sólo
`docker compose` v2. La lógica cross-platform/Podman existente se conserva únicamente por
paridad con el oráculo TS y no ofrece garantía. El trabajo pendiente por ítem vive en
[REMAINING-WORK.md](REMAINING-WORK.md).

El backlog detallado y priorizado se mantiene únicamente en
[REMAINING-WORK.md](REMAINING-WORK.md).

## Resumen

| Área | Estado |
|---|---|
| Core: `up` / `build` / `exec` / `read-configuration` / `run-user-commands` / `set-up` | ✅ paridad, validado por la matriz runtime |
| Comportamientos 0.88 (workspace-folder→cwd, metadata array, consistency, lockfile, …) | ✅ cerrados |
| `outdated` / `upgrade` | ✅ byte-idéntico |
| `features info` (manifest/tags/dependencies, texto + json) | ✅ byte-idéntico |
| `features` / `templates` `generate-docs` | ✅ byte-idéntico |
| `features package` → `devcontainer-collection.json` | ✅ byte-idéntico |
| `features` / `templates` `resolve-dependencies` (grafo + installOrder) | ✅ idéntico (post-scrub de hashes) |
| Features `dependsOn` transitivo durante instalación | ✅ resuelve, deduplica y ordena dependencias; ciclos abortan |
| `features` / `templates` `publish` (push a registry OCI) | ✅ idéntico (test con `registry:3` vía testcontainers) |
| `templates metadata` | ✅ byte-idéntico (orden de keys preservado) |
| `features test` (build + run de scripts por feature) | 🟡 implementación endurecida; E2E A/B agregado, pendiente de ejecución Docker |
| Seguridad: `disallowed-features` (blocklist del control-manifest) | ✅ cableado, envelope idéntico |

## Gates inmediatos

Este resumen no reemplaza el backlog. Los gates inmediatos antes de declarar
paridad completa son:

1. **Validación y refinamientos de `features test`** — los errores de preparación ya
   se distinguen de tests fallidos y escenarios omitidos, y existe un caso E2E A/B
   (`features.test-single-scenario-success`). Falta ejecutarlo/promoverlo a `match`
   en un runner con Docker/red. Tampoco genera todavía los tests
   *autogenerados/randomizados* ni colorea la salida (ANSI).
2. **Digest del tarball de `features package`** — **no es alcanzable** la paridad
   byte-a-byte: los headers del tar incrustan `mtime` y node-tar/Go difieren en su
   manejo (el propio TS es no-determinista). El contenido, el file-list y el
   `collection.json` sí coinciden. No es un gap real de comportamiento.
3. **Higiene de aislamiento en la matriz** — algunos casos compose son *flaky* bajo
   ejecución paralela (comparten recursos docker); pasan aislados. Mejora de test, no
   de producto.
4. **Corrida limpia de release** — falta una ejecución CI sin `failed` ni
   `inconclusive`, con los JSON de contract/network/runtime y el SHA del oráculo
   guardados como artefactos. El workflow ya produce esos archivos.

No hay divergencias de producto abiertas conocidas en los comandos core ni en los
auxiliares cubiertos.

## Cómo se valida

- **`docs/migration/parity-matrix.yaml`** + **`internal/cli/parity_matrix_test.go`**
  (`TestParityMatrix`): ~170 casos que corren el mismo comando por el binario Go y el
  oráculo TS y comparan salida/estado. Lanes: `contract` (sin docker) y `runtime`
  (`PARITY_LANE=all`). Cada caso documenta su intención en `notes:`.
- El harness está **endurecido** contra falsos positivos:
  - los casos `-success` fallan si Go no llega a exit 0 con TS en 0 (W1);
  - Go siempre corre aunque TS se salte, y los skips se loguean con `[case=…]` (W6);
  - la normalización no coerciona escalares JSON ni scrubbea de más (W3).
  - cada corrida publica conteos observados de `matched`, `failed`,
    `skipped-docker`, `skipped-network`, `inconclusive` y `not-selected`; no se
    infiere cobertura a partir de que el paquete haya terminado verde;
  - CI usa `PARITY_STRICT=true`: cualquier caso inconcluso en el lane seleccionado
    hace fallar el gate. Los skips de capacidades deshabilitadas explícitamente se
    reportan por separado.

```sh
task parity:contract  # contrato hermético
task parity:semantic  # semántica sin Docker/red
task parity:network   # casos que requieren red externa
task parity:runtime   # matriz completa (requiere Docker)
```

## Mantener la paridad al evolucionar

1. Al refactorizar Go, correr la matriz para confirmar que no se coló un cambio de
   comportamiento no intencional.
2. Al cambiar comportamiento a propósito, actualizar el caso de la matriz (o quitarlo)
   — la matriz documenta las divergencias deliberadas.
3. Para seguir una versión upstream nueva: bump del submódulo `reference/` y re-correr;
   los fallos nuevos marcan lo que upstream movió.
