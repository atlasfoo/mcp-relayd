# Flujo actual de GitHub Actions

Este documento describe el orden implementado en `checks.yml`,
`pipeline-checks.yml`, `build.yml`, `release.yml` y `justfile`. Los checks
locales no verifican el comportamiento real de `skip ci`, bypass de reglas,
caché o artifacts en GitHub alojado. La confirmación alojada anterior es
anterior al refactor y no valida este pipeline.

## Cadena

```mermaid
flowchart TD
    PR[Pull request] --> CPR[Checks: valida app; restaura; no guarda]
    PR -->|Cambios de pipeline| PCR[Pipeline Checks: suite aislada]
    PUSH[Push propio a master] --> CMP[Checks: valida app; restaura; guarda si falta]
    PUSH -->|Cambios de pipeline| PCP[Pipeline Checks: suite aislada]
    CMP -->|Éxito; evento push propio a master| BUILD[Build workflow_run]
    CPR --> END[Sin build/publicación]
    PCR --> END
    PCP --> END
    CMP -->|PR u otro origen| END
    BUILD -->|Candidato empaquetado; artifact cargado| PUSHJOB[Push privilegiado]
    BUILD -->|No elegible: termina antes de empaquetar/artifact| END
    PUSHJOB -->|Preflight válido y refs vigentes| APP[Token App limitado al repositorio]
    PUSHJOB -->|No listo| END
    APP -->|Push atómico confirmado| RELEASE[Release reusable en el mismo run]
    RELEASE --> PUBLISH[Verificar artifacts exactos y publicar/recuperar draft]
```

`Checks` valida el SHA exacto, commits/título y calidad de aplicación; sube
metadata de procedencia. `Pipeline Checks` es independiente y path-filtered:
valida cambios de infraestructura, pero no produce releases ni dispara Build.
El Build escucha exclusivamente el workflow `Checks` y se dispara con
`workflow_run`, pero solo su job corre si
Checks fue exitoso y provino de un `push` propio a `master`. Antes de empaquetar,
Build verifica por API el run y artifact de Checks; usa tooling del checkout
confiable (`trusted/`) y compila el SHA fuente en `source/`.

Si no hay `feat`/`fix`, no hay versión nueva, o el commit es el bump propio
canónico, Build termina sin empaquetar candidato publicable. Para un candidato,
Build calcula la versión, crea localmente el commit/tag de bump con sufijo
`[skip ci]` y empaqueta seis binarios ligados al SHA fuente y a ese bump. Sube
el artifact al run/attempt productor. No obtiene token App durante la
compilación.

## Checks: validación y caché

```mermaid
flowchart TD
    C0[PR o push a master] --> C1[Checkout del SHA fuente exacto e historial completo]
    C1 --> C2[Restaurar nix-env-v2; preparar permisos del runner]
    C2 --> C3[Instalar Nix fijado y bootstrap devenv fijado]
    C3 --> C4[devenv shell: provisiona mcp-proxy]
    C4 --> C5[ci-checks: valida evento, HEAD, base y commits/título]
    C5 --> C6[Gate app: check, go vet ./..., go test internal process/gateway/integration -count=1]
    C6 --> C7[Subir checks-metadata de procedencia]
    C7 --> C8{¿Push a master y cache key no existe?}
    C8 -->|Sí| C9[Exportar NARs y guardar caché compartida]
    C8 -->|No| C10[No escribe caché]
    C9 --> C11[Checks finalizado]
    C10 --> C11
```

El gate local de app ejecuta `check`, `go vet ./...` y tests de integración
para `internal/process`, `internal/gateway` e `internal/integration` con
`-count=1`; el run sin caché evita reutilizar resultados de esos tests. No
valida infraestructura. Para pedir esa validación local, ejecutar
`just check-pipeline`; `just check-changes [BASE HEAD]` selecciona el gate de
pipeline junto con el de app según los paths cambiados. La suite de pipeline es
el módulo Go anidado `tests/release` (fmt, lint, vet y tests), `actionlint` y
`unittest discover` con los 25 tests Python actuales en `tests/pipeline`. No
ejecuta validación de caché. El workflow `Pipeline Checks` es independiente, activado
por cambios de pipeline en PR/master, y no dispara Build. El artifact metadata
vincula SHA, repo, evento y run/attempt. Solo el push
exitoso a `master` guarda, y solo si no hubo cache hit exacto; PR siempre es
lector. `workflow_run` de Build filtra adicionalmente por éxito, `push`, rama
`master` y repo propio antes de asignar runner.

## Build: validación, candidato y artifact inerte

```mermaid
flowchart TD
    B0[Checks exitoso elegible] --> B1[Checkout workflow SHA en trusted/]
    B1 --> B2[Restaurar caché compartida; bootstrap Nix/devenv]
    B2 --> B3[Descargar checks-metadata del run disparador]
    B3 --> B4[API: verificar Checks, artifact, SHA, run y attempt]
    B4 --> B5[Checkout SHA verificado en source/]
    B5 --> B6[ci-build con justfile trusted]
    B6 --> B7{¿Origen, bump propio, elegibilidad y refs válidos?}
    B7 -->|No elegible / bump propio / stale| BEND[Exit exitoso sin paquete, artifact ni Release]
     B7 -->|Candidato nuevo| B8["Commitizen local: crea commit bump y tag; commit lleva [skip ci]"]
    B7 -->|Recuperación tras rerun| B9[Reusar bump/tag exactos; baseline desde tags de source]
    B8 --> B10[Paquetizar 6 binarios y verificar hashes/manifiesto]
    B9 --> B10
    B10 --> B11[Crear bump.bundle; incluir metadata de cadena]
    B11 --> B12[Subir artifact release-run-attempt; conservar artifact ID y attempt productor]
    B12 --> B13[Build producer exitoso]
```

El commit/tag creado es local al runner: hasta terminar upload, ningún ref
remoto cambia. El bundle es handoff de objetos Git, no código ejecutable. El
job `push` solo depende de Build/artifact exitoso y elegible; una omisión de
elegibilidad no crea artifact de release ni invoca Release.

## Push: preflight sin escritura, luego refs atómicos

```mermaid
flowchart TD
    P0[Build artifact ID y attempt] --> P1[Checkout trusted; restore cache; bootstrap]
    P1 --> P2[Descargar por artifact ID del mismo Build run]
    P2 --> P3[API: validar caller, producer exitoso y attempt productor exacto]
    P3 --> P4[Verificar metadata, hashes y bump.bundle]
    P4 --> P5[Fetch master/tags; confirmar source, baseline y refs vigentes]
    P5 --> P6{¿Preflight listo?}
    P6 -->|No| PEND[Termina sin token App ni mutación remota]
    P6 -->|Sí| P7[Emitir token App repo-específico; Contents write]
    P7 --> P8[Revalidar refs y handoff con tooling trusted]
    P8 --> P9{¿Refs cambiaron o push falla?}
    P9 -->|Sí| PFAIL[Job falla; no se invoca Release]
    P9 -->|No| P10[Git push --atomic de master y tag]
    P10 --> P11[Confirmar refs remotos; pushed=true]
    P11 --> P12[Invocar Release reusable en el mismo Build run]
```

El productor es el run que subió el artifact, no necesariamente el intento
actual del caller reejecutado. API confirma producer exitoso y artifact
perteneciente al ID/run/attempt retenidos; el caller puede estar `in_progress`,
porque Release corre como parte de ese mismo Build. Si el push atómico falla,
Release no corre y no se publica parcialmente solo uno de los refs.

## Release reusable: publicar bytes ya asociados a refs

```mermaid
flowchart TD
    R0[Caller Build: pushed=true] --> R1[workflow_call con artifact_id y producer_attempt]
    R1 --> R2[Checkout trusted; restaurar caché; bootstrap Nix/devenv]
    R2 --> R3[Descargar artifact ID exacto del mismo run]
    R3 --> R4[API: validar Build, Checks, caller, productor y cadena]
    R4 --> R5[Verificar manifiesto, archivos y hashes locales]
    R5 --> R6[Leer identidad pública y validar master/tag/bump actuales sin escritura]
    R6 --> R7{¿Preflight válido y refs publicadas?}
    R7 -->|No| REND[Fin sin token de publicación]
    R7 -->|Sí| R8[Emitir token App repo-específico; Contents write]
    R8 --> R9[Revalidar; crear o recuperar draft y assets exactos]
    R9 --> R10[Verificar bytes remotos; publicar draft completo]
```

Release no ejecuta Commitizen, no crea bump/tag y no hace `git push`. Su
reintento usa el mismo artifact del productor; recupera un draft/asset parcial
si los refs siguen válidos. Para Build reintentado, `ci-build` conserva el
baseline derivado de tags alcanzables desde el SHA fuente y recupera el bump
ya publicado, evitando una versión duplicada.

El job `push` separado valida API, artifact y refs actuales sin credencial de
escritura. Solo después del preflight obtiene un token GitHub App limitado a
este repositorio y `Contents: write`, vuelve a validar y hace push atómico de
los objetos verificados. Release se invoca mediante `workflow_call` dentro del
mismo workflow Build únicamente si el push terminó con `pushed=true`; no tiene
disparador ni concurrencia independiente. Recibe explícitamente solo la clave
privada necesaria para emitir su propio token de App, valida de nuevo la
procedencia y descarga por ID el artifact exacto del run y attempt productor.
No ejecuta Commitizen ni crea commits/tags/push. Publica o recupera un draft
parcial verificando los bytes de los assets.

La concurrencia es única por repositorio en el caller Build y no cancela
publicaciones en curso. Si falla la publicación parcial, reintenta el mismo
Build; si se reejecuta el productor, recupera el bump/ref exacto de esa fuente
en vez de calcular otra versión. Un candidato obsoleto no se autoriza con un
reintento de Release antiguo: hacen falta Checks y Build nuevos para el `master`
actual.

## `skip ci`, permisos y bypass

El sufijo `[skip ci]` del commit de bump evita iniciar otra ejecución
normal de Checks. GitHub puede dejar en pendiente los checks requeridos cuando
se omite el workflow; no se debe depender de ese commit como único mecanismo
para satisfacer una regla de protección. El reconocimiento estructural del
bump propio en Checks es un fallback seguro si el workflow llega a ejecutarse,
no una omisión de validación basada solo en el texto del mensaje.

Los workflows comienzan con permisos mínimos: `contents: read` y `actions:
read` donde se requiere leer APIs/artifacts. Solo los jobs privilegiados
`push` y `release` solicitan cada uno su token App repositorio-específico y
`Contents: write`, tras su preflight sin escritura. Las credenciales estándar
de checkout no persisten; no se pasa `secrets: inherit`.

Prerequisitos para publicar: instalar la App en el repositorio con
`Contents: write`; definir `RELEASE_APP_CLIENT_ID`, secreto
`RELEASE_APP_PRIVATE_KEY` y variable pública `RELEASE_BOT_SLUG`. Si ruleset o
protección de `master` impide el push de la App, un administrador debe
configurar el bypass correspondiente para la identidad de la App. Eso es una
configuración externa obligatoria; este workflow no la crea ni la verifica
antes de una ejecución alojada.

## Caché NAR compartida

Checks, Build y Release restauran `nix-env-v2` con OS, arquitectura y hash de
`devenv.nix`, `devenv.yaml` y `devenv.lock` (Build/Release usan `trusted/`).
Solo Checks guarda, después de un push exitoso a `master`, y solo si la clave
no existe. Pull requests, Build y Release son lectores. Es una caché de NAR,
no un `/nix/store` compartido; caché fría debe funcionar. Cada entrada a
`devenv shell` sigue provisionando `mcp-proxy` por separado. La reutilización
entre workflows y runners alojados aún no está confirmada.

## Tooling confiable y límites de validación

- Build/Push/Release usan `justfile` y `tooling/release.py` del checkout
  confiable (`trusted/`), no del checkout de source; Python se ejecuta aislado
  con `python -I`. El checkout `source/` proporciona el SHA de aplicación.
- PR y builds de desarrollo no producen paquetes. Release rechaza una llamada
  no publicable.
- `act` es una posibilidad futura, no una decisión adoptada ni un smoke test
  ejecutado. Solo se deberían sustituir tests redundantes por una prueba con
  cobertura real equivalente. La emulación no valida permisos de GitHub ni la
  cadena de workflows y no reemplaza validación alojada.

La suite raíz de Go solo valida la aplicación: la infraestructura requiere
`just check-pipeline` o `just check-changes`. No se reclama evidencia alojada
del pipeline refactorizado.
