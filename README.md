# mcp-relayd

Daemon Go local que expone servidores MCP stdio mediante un bridge externo. El
entorno de desarrollo es opcional y está gestionado por
[devenv](https://devenv.sh/getting-started/) y Nix.

## Runtime local (fase 1)

`mcp-relayd` no incluye el bridge en sus binarios: cada servidor configurado
usa un proceso externo `mcp-proxy` independiente. La versión soportada es
`mcp-proxy 0.12.0` con `MCP 1.30.0`; el lock de
[`tooling/mcp-proxy/requirements.lock`](tooling/mcp-proxy/requirements.lock)
fija las dependencias transitivas y sus hashes.

### Instalar el bridge

Al entrar en `devenv shell`, Python 3.13 y uv provisionan automáticamente el
bridge en un entorno dedicado `.devenv/mcp-proxy` desde ese lock.
Fuera de devenv, instala Python 3.13 y uv, y desde la raíz del repositorio
ejecuta el mismo provisioner:

```sh
python3.13 scripts/provision-mcp-proxy.py
```

En Windows, invoca el script con Python 3.13 (por ejemplo,
`py -3.13 scripts/provision-mcp-proxy.py`). El provisioner crea o recrea
`.devenv/mcp-proxy`, instala únicamente wheels con hashes del lock y comprueba
la versión. Añade su directorio `bin` (Unix) o `Scripts` (Windows) al `PATH`,
o configura `proxy.command` en el TOML con la ruta absoluta al ejecutable.
Requiere acceso a PyPI durante la provisión inicial. No uses un entorno Python
compartido para `--venv`, porque el directorio seleccionado se recrea.

### Configuración y arranque

El archivo predeterminado es global por usuario, independiente del directorio
actual: `~/.config/mcp-relayd.toml` en Linux y macOS, y
`%USERPROFILE%\.config\mcp-relayd.toml` en Windows. Crea ese directorio y
copia [`examples/mcp-relayd.toml`](examples/mcp-relayd.toml) allí. Para usar
otra ubicación, pasa `--config PATH` explícitamente; las rutas relativas de
ese override se resuelven desde el directorio actual. Si el archivo elegido
no existe o no es válido, el daemon falla; no crea configuración ni busca un
archivo alternativo.

```sh
mcp-relayd run
# O bien:
mcp-relayd run --config ./mcp-relayd.toml
```

Los campos `command` y `args` de cada servidor se ejecutan como programa y
argumentos separados, sin shell. La expansión `${VARIABLE}` se aplica solo a
los valores dentro de `[servers.<nombre>.env]`; no se expande en comandos,
argumentos, rutas ni otros campos. El gateway escucha por defecto en
`127.0.0.1:9876`; `GET /health` informa el estado y
`/mcp/<nombre>` enruta al servidor. Host y Origin se restringen al gateway
local (Origin puede omitirse). Cada servidor conserva un proxy y una sesión
upstream compartidos entre las solicitudes de sus clientes; nombres distintos
tienen rutas y procesos separados. Los logs estructurados JSON se escriben en
stderr. Al recibir una señal de cierre, el daemon drena HTTP y termina los
árboles de procesos con un presupuesto global acotado, forzando el cierre si
vence el plazo.

`autostart = false` mantiene ese servidor detenido; no hay arranque lazy ni
activación por solicitud. Para iniciarlo, cambia la configuración y reinicia
el daemon. Esta fase no ofrece reinicio automático, instalación de servidores
MCP, servicio/autostart del sistema operativo, exposición LAN, bridge nativo,
ni garantía completa de funciones server-to-client. Las capacidades MCP
dependen de la versión fijada de `mcp-proxy`. La automatización de workflows,
builds y releases prevista para fase 5 aún no forma parte del runtime.

## Empezar con Nix

Nix y devenv deben estar instalados. Si devenv no aparece en el PATH:

```sh
export PATH="$HOME/.nix-profile/bin:$PATH"
```

Desde esta carpeta:

```sh
devenv shell
run run
```

El helper `run` ejecuta `go run ./cmd/mcp-relayd` y reenvía sus argumentos;
por eso el segundo `run` selecciona el subcomando del daemon. Con Go fuera de
devenv, invócalo directamente como `go run ./cmd/mcp-relayd run`.

La primera activación descarga Go, gopls, Git, golangci-lint, Lefthook, typos,
Commitizen, Python 3.13 y uv mediante Nix. `devenv.lock`
fija las revisiones. Guarda ese archivo en el repositorio para que otros
desarrolladores usen las mismas revisiones.

Dentro del entorno:

```sh
fmt         # corrige formato e imports de Go
lint        # comprueba formato y análisis estático sin modificar archivos
spellcheck  # comprueba ortografía inglesa y nombres de archivos
check       # lint, ortografía y go test
build       # genera bin/mcp-relayd
```

También puedes ejecutar la comprobación del entorno desde fuera del shell:

```sh
devenv test
```

La tarea `mcp-relayd:quality` ejecuta `check` y comprueba la salida del
programa antes de `devenv:enterTest`; así las verificaciones también se
ejecutan con devenv 2.4.

## Hooks y commits

Commitizen (`cz`) ya está disponible dentro de `devenv shell`. Activa los
hooks una vez por clon:

```sh
lefthook install
```

Ejecuta los commits y pushes dentro de `devenv shell`
para que los hooks encuentren las herramientas y el comando `check`.

1. `pre-commit` aplica `gofumpt` y `goimports` a los archivos Go preparados
   para el commit y vuelve a añadir esos archivos al índice. Después comprueba
   la ortografía de los archivos preparados.
2. `commit-msg` rechaza mensajes que no sigan Conventional Commits.
3. `pre-push` ejecuta `check` sobre todo el proyecto sin modificar archivos.

El hook de formato vuelve a añadir el archivo Go completo: si preparaste
solo algunos fragmentos, revisa el índice antes de confirmar el commit.

Puedes preparar un mensaje interactivamente o usar Git directamente:

```sh
cz commit
# Alternativa:
git commit -m "feat(relay): expose local MCP servers"
```

`.golangci.yml` es la autoridad de formato y análisis estático. Usa la base
estándar de golangci-lint v2 con comprobaciones de errores, seguridad, recursos
HTTP, contextos e idioms modernos; [la configuración oficial](https://golangci-lint.run/docs/configuration/file/)
explica las secciones de linters y formateadores.

`.typos.toml` revisa inglés y contiene el vocabulario del proyecto. Excluye
el README en español, archivos generados conocidos, dependencias, archivos de
credenciales y directorios de entorno o salida. `--force-exclude` conserva
estas exclusiones incluso cuando el hook recibe rutas explícitas.

## Versiones y releases locales

`.cz.toml` almacena la versión inicial `0.1.0`, usa SemVer 2 y etiquetas
`v<version>`. Antes de preparar una release, deja el árbol de trabajo limpio:

```sh
cz version --project
cz bump --dry-run
# Después de revisar el resultado:
cz bump
```

`cz bump` modifica la versión, genera `CHANGELOG.md`, crea un commit de
release y una etiqueta local; no publica ni hace push. Si aún no existe una
etiqueta de versión, Commitizen pide confirmar que es la primera release.
`feat` incrementa la versión menor y `fix` la corrección. Con
`major_version_zero = true`, los cambios incompatibles incrementan la versión
menor durante `0.x`; retira esa opción al preparar `1.0.0`. Véase
[la documentación de bump](https://commitizen-tools.github.io/commitizen/commands/bump/).

## Estructura

```text
cmd/mcp-relayd/main.go  # punto de entrada
go.mod                 # módulo Go
devenv.nix             # herramientas y comandos
devenv.yaml            # origen de los paquetes Nix
.golangci.yml          # formato y análisis estático de Go
.typos.toml            # ortografía y vocabulario
.cz.toml               # commits y versión SemVer
lefthook.yml           # hooks locales de Git
```

Cuando exista lógica propia, colócala en `internal/` y mantén el punto de
entrada pequeño. El nombre local del módulo es `mcp-relayd`; cuando publiques
el código, puedes sustituirlo por la ruta del repositorio.

## Dependencias y actualizaciones

La versión efectiva de Go proviene de los paquetes fijados en `devenv.lock`.
`go 1.25.0` en `go.mod` declara la versión mínima del lenguaje. El entorno
usa `GOTOOLCHAIN=local` para que Go no descargue otra herramienta por su cuenta.

Añade dependencias de sistema a `packages` en `devenv.nix`, por ejemplo
`pkgs.pkg-config` si una futura biblioteca nativa lo necesita. No hace falta
instalarlas globalmente.

Para actualizar las revisiones de Nix de forma intencional:

```sh
devenv update
devenv test
```

## Usar Go sin Nix

Si prefieres tu propia instalación de Go (1.25 o posterior), instala también
golangci-lint 2.13 o posterior, Lefthook, typos y Commitizen. Las versiones probadas
son golangci-lint 2.13.2, Lefthook 2.1.12, typos 1.50.1 y Commitizen 4.16.5; Nix las suministra
desde las revisiones de `devenv.lock`.

Ejecuta las mismas verificaciones directamente:

```sh
go run ./cmd/mcp-relayd run
golangci-lint fmt --diff ./...
golangci-lint run ./...
typos --force-exclude .
go test ./...
go build -o bin/mcp-relayd ./cmd/mcp-relayd
```

Los hooks usan los comandos del entorno devenv; usa `devenv shell` cuando
vayas a instalar o ejecutar los hooks.

## Workflows, builds y distribución

Los workflows usan el entorno declarativo de Nix/devenv: `devenv.nix`,
`devenv.yaml` y `devenv.lock` fijan herramientas y revisiones. En cada runner
de GitHub Actions se instala la versión fijada de Nix y se construye devenv
desde su revisión bloqueada. Checks, Build y Release comparten una caché binaria
NAR bajo `nix-env-v2`; su clave incluye sistema, arquitectura y el hash de esos
tres archivos de configuración. No comparten una base viva de `/nix/store`.
Checks es el único escritor: exporta y guarda tras un push exitoso a `master`,
si la clave todavía no existe. Los pull requests, Build y Release solo restauran
la caché. Build y Release calculan el hash desde su checkout de tooling confiable
en `trusted/`; si el entorno difiere, la clave también cambia. Una caché fría no
debería cambiar la corrección; su reutilización entre workflows alojados sigue
pendiente de verificación. La provisión Python de `mcp-proxy` no forma parte de
esta caché NAR y sigue ejecutándose al entrar en el shell.

La secuencia predeterminada conecta tres workflows; Release es reusable y se
ejecuta dentro del mismo run de Build:

1. **Checks** corre en pull requests y pushes a `master`, valida el SHA exacto
   del código, los commits/título y las comprobaciones. No necesita la GitHub
   App para los builds de desarrollo.
2. **Build** solo ejecuta su job tras Checks exitoso de un `push` propio a
   `master` (`workflow_run`); los pull requests ejecutan únicamente Checks.
   Verifica la ejecución y sus metadatos mediante la API y empaqueta el SHA
   comprobado. El `push` original a `master` se verifica en la ejecución de
   Checks consultada por API: el evento de Build en sí es `workflow_run`, no
   `push`.
3. **Release** (`workflow_call`) solo se llama tras el push privilegiado
   exitoso del job `push` de Build. Verifica la cadena y descarga el artifact
   exacto por ID; no tiene disparador ni concurrencia independiente.

El [diagrama Mermaid paso a paso](docs/actions-flow.md) muestra el orden actual,
las decisiones de publicación y los puntos pendientes de refinamiento.

Build produce seis archivos: `mcp-relayd_<versión>_{linux,darwin,windows}_
{amd64,arm64}.{tar.gz,zip}` (Windows usa ZIP; Linux y macOS, tar.gz), más un
manifiesto con hashes SHA-256 y metadatos del origen. Los binarios Go son
cruzados con `CGO_ENABLED=0`; no incluyen `mcp-proxy`, que sigue siendo un
componente externo. El Release público contiene esos seis paquetes y
`SHA256SUMS`. Descárgalos desde **Releases** del repositorio y verifica los
paquetes con `sha256sum -c SHA256SUMS` (en macOS puede usarse
`shasum -a 256 -c SHA256SUMS`).

Una fuente elegible requiere al menos un `feat` o `fix` desde la última
etiqueta, incluso si el último push solo cambia docs/CI. Sin incremento, Build
termina correctamente antes de crear bump, paquetes o artifacts y no llama a
Release. Un fallo de compilación o upload tampoco permite publicar refs remotas.
Build calcula la versión con Commitizen, crea localmente el commit y
tag de bump canónicos con `[skip ci]` y empaqueta los seis binarios. El job
`push` publica únicamente los refs verificados; Release publica después los
assets, sin ejecutar Commitizen ni mutar Git. Un bump propio no inicia otro
ciclo de build; los candidatos obsoletos requieren nuevos Checks y Build.
Reintenta el mismo Build para recuperar una publicación parcial cuando sus
refs siguen vigentes. Los checks requeridos pueden quedar pendientes tras el
skip nativo; el bypass de las reglas de `master` para la App es un prerequisito
externo, además de instalarla con `Contents: write` y configurar sus variables
y secreto como se describe abajo. La publicación se serializa por repositorio
y no cancela ejecuciones en curso.

### Configuración de la GitHub App

Para una release elegible configura estas variables/secretos del repositorio:

| Nombre | Tipo | Uso |
| --- | --- | --- |
| `RELEASE_APP_CLIENT_ID` | Variable | Client ID/App ID de la GitHub App, pasado al input `app-id` de la acción de token. |
| `RELEASE_APP_PRIVATE_KEY` | Secreto | Clave privada de la App; no la pegues en código, issues ni logs. |
| `RELEASE_BOT_SLUG` | Variable pública | Slug de la App; identifica commits de bump propios y permite reconocer/reanudar una publicación. |

Instala la App únicamente en este repositorio con permiso **Contents: write**.
Si las reglas de protección/ruleset de `master` bloquean el push automatizado,
un administrador debe configurar manualmente el bypass correspondiente para
la identidad de la App. No se requiere pegar claves ni tokens en el ruleset.
La identidad pública inicial de los commits de bump se obtiene del slug; en
la publicación, la ejecución deriva la identidad del `app-slug` real del token
de App y su API pública. Por eso `RELEASE_BOT_SLUG` es necesario para la
supresión y recuperación correctas, además de configurar el ID y la clave.

### Recetas locales de build y verificación

Ejecuta las recetas en `devenv shell` desde la raíz. Las recetas de candidato,
preflight y verificación solo escriben el JSON de salida solicitado; no
publican. Define argumentos con valores apropiados para tu checkout:

```sh
# Inspeccionar si HEAD produciría una versión, sin cambiar refs ni archivos fuente.
just release-candidate /tmp/candidate.json

# Empaquetar seis targets con la versión/SHA/run elegidos y producir manifiesto.
mkdir -p /tmp/opencode
VERSION=0.1.0
SOURCE_SHA="$(git rev-parse HEAD)"
RUN_ID=local
ASSETS=/tmp/opencode/mcp-relayd-assets
just release-package "$VERSION" "$SOURCE_SHA" "$RUN_ID" "$ASSETS" /tmp/opencode/package.json

# Validar los seis archivos contra manifest.json, sin extraerlos.
just release-verify "$ASSETS" /tmp/opencode/verified.json

# Preflight de refs ya descargadas; solo informa si source y última etiqueta siguen actuales.
LAST_TAG="$(git describe --tags --abbrev=0 2>/dev/null || true)"
just release-preflight "$SOURCE_SHA" "$LAST_TAG" /tmp/opencode/preflight.json
```

`release-preflight` requiere refs actualizadas previamente (`origin/master` y
tags); no las descarga. Para validar localmente un handoff completo ya
empaquetado usa `ci-push` con provenance, assets y salida, y
`RELEASE_PREFLIGHT_ONLY=1`: verifica artifact/ref/bundle y consulta el remoto,
pero no hace push. Requiere permisos de lectura del remoto y no necesita
credenciales de escritura. Sin esa variable, solo el job CI `push` usa `ci-push`
para publicar atómicamente `master` y el tag tras el preflight y una nueva
validación con token App. `ci-release` es publicación-only: comprueba los refs
ya publicados y crea/recupera el draft y sus assets; no hace bump, tag ni push
de Git. No ejecutes la ruta de publicación local como receta casual ni con
credenciales sin protección. En GitHub, la publicación elegible está separada
tras el preflight sin App.

La primera activación de Nix/dev-env y los binarios cruzados macOS/Windows no
se han validado en runners nativos; la compilación cruzada Go se ejecuta en
Linux. La verificación real de la identidad/instalación de App, cachés y
artefactos de Actions queda pendiente de una ejecución alojada después del
merge.
