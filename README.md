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
