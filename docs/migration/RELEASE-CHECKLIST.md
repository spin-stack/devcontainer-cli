# Checklist de release y declaración de paridad

Esta es la única checklist de salida para declarar que el CLI Go está en paridad
con el oráculo TypeScript fijado en `reference/`. El estado narrativo vive en
[`GO-REWRITE-STATUS.md`](GO-REWRITE-STATUS.md).

La implementación pendiente se gestiona en
[`REMAINING-WORK.md`](REMAINING-WORK.md); esta checklist sólo decide si un commit
candidato puede liberarse.

## Identidad del release

- [ ] `reference/` apunta al tag objetivo esperado (actualmente v0.88.0).
- [ ] El SHA exacto del submódulo queda guardado en `reference-commit.txt`.
- [ ] El árbol de trabajo usado por CI corresponde al commit/tag candidato.
- [ ] El binario informa la versión esperada y los cross-builds terminan correctamente.

## Calidad básica

- [ ] `task lint` pasa.
- [ ] `task coverage` pasa y `coverage.out` queda guardado como artefacto.
- [ ] `task test:integration` pasa en un runner que permite listeners locales.
- [ ] `task test:e2e` pasa con Docker y no deja containers etiquetados.
- [ ] No hay regresiones nuevas sin test en CLI, OCI, lifecycle o Docker/Compose.

## Paridad observable

- [ ] `task parity:contract` termina sin `failed` ni `inconclusive`.
- [ ] `task parity:network` termina sin `failed` ni `inconclusive`.
- [ ] `task parity:runtime` termina sin `failed` ni `inconclusive`.
- [ ] Todo caso seleccionado termina como `matched`; los skips de capacidades están
      explicados y no afectan el lane obligatorio.
- [ ] Los casos `deferred-runtime` fueron ejecutados y su estado YAML actualizado a
      partir de evidencia, no por edición declarativa anticipada.
- [ ] Publish de features y templates fue comparado contra `registry:3`, incluyendo
      tags, manifests y metadata de colección.
- [ ] `features test` ejecutó al menos un escenario real A/B y verificó cleanup.

## Artefactos y decisión

- [ ] CI conservó `parity-contract.json`, `parity-network.json`,
      `parity-runtime.json`, `reference-commit.txt` y `coverage.out`.
- [ ] Los JSON suman todos los casos de la matriz entre `matched`, `failed`,
      `skipped-docker`, `skipped-network`, `inconclusive` y `not-selected`.
- [ ] No se usó un `PASS` con casos omitidos como evidencia de paridad.
- [ ] Las divergencias deliberadas están documentadas en el estado vigente.
- [ ] Sólo después de completar todos los puntos anteriores se cambia el estado a
      “paridad completa” y se crea el release.

## Comandos locales equivalentes

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
