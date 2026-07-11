# Estado de paridad — Go CLI vs devcontainers/cli 0.88

Estado **actual** de la paridad del CLI Go contra el oráculo TypeScript (submódulo
`reference/`, v0.88.0). Este documento describe *dónde estamos y qué falta*, no la
historia de cómo llegamos — para eso está el `git log` y
[`AUDIT-2026-07-09.md`](AUDIT-2026-07-09.md) (auditoría inicial que arrancó el trabajo).

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
| Seguridad: `disallowed-features` (blocklist del control-manifest) | ✅ cableado, envelope idéntico |

## Qué falta

Poco, y de bajo impacto:

1. **Cobertura de `publish` / `test`** — `features publish`, `features test`,
   `templates publish`, `templates metadata` no tienen casos de paridad todavía.
   `publish` necesita un registry OCI local para un caso hermético; `features test`
   levanta contenedores por escenario. La implementación existe; falta el harness.
2. **Digest del tarball de `features package`** — **no es alcanzable** la paridad
   byte-a-byte: los headers del tar incrustan `mtime` y node-tar/Go difieren en su
   manejo (el propio TS es no-determinista). El contenido, el file-list y el
   `collection.json` sí coinciden. No es un gap real de comportamiento.
3. **Higiene de aislamiento en la matriz** — algunos casos compose son *flaky* bajo
   ejecución paralela (comparten cache/proyecto); pasan aislados. Mejora de test, no
   de producto (ver W6 en la sección de harness abajo).

No hay divergencias de producto abiertas conocidas en los comandos core ni en los
auxiliares cubiertos.

## Cómo se valida

- **`docs/migration/parity-matrix.yaml`** + **`internal/cli/parity_matrix_test.go`**
  (`TestParityMatrix`): ~170 casos que corren el mismo comando por el binario Go y el
  oráculo TS y comparan salida/estado. Lanes: `contract` (sin docker) y `runtime`
  (`PARITY_LANE=all`). Cada caso documenta su intención en `notes:`.
- **`docs/migration/compare-parity.sh`**: comparación rápida de `read-configuration`
  por cada fixture.
- El harness está **endurecido** contra falsos positivos:
  - los casos `-success` fallan si Go no llega a exit 0 con TS en 0 (W1);
  - Go siempre corre aunque TS se salte, y los skips se loguean con `[case=…]` (W6);
  - la normalización no coerciona escalares JSON ni scrubbea de más (W3).

```sh
task parity                       # compare-parity.sh (read-configuration por fixture)
PARITY_LANE=all task parity:matrix  # matriz completa (requiere docker)
```

## Mantener la paridad al evolucionar

1. Al refactorizar Go, correr la matriz para confirmar que no se coló un cambio de
   comportamiento no intencional.
2. Al cambiar comportamiento a propósito, actualizar el caso de la matriz (o quitarlo)
   — la matriz documenta las divergencias deliberadas.
3. Para seguir una versión upstream nueva: bump del submódulo `reference/` y re-correr;
   los fallos nuevos marcan lo que upstream movió.
