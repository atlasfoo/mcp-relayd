# mcp-relayd

Proyecto Go inicial con un entorno de desarrollo opcional gestionado por
[devenv](https://devenv.sh/getting-started/) y Nix.

## Empezar con Nix

Nix y devenv deben estar instalados. Si devenv no aparece en el PATH:

```sh
export PATH="$HOME/.nix-profile/bin:$PATH"
```

Desde esta carpeta:

```sh
devenv shell
run
```

El programa imprime `Hello from mcp-relayd!`. La primera activación descarga
Go, gopls, Git, golangci-lint, Lefthook, typos y Commitizen mediante Nix. `devenv.lock`
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
go run ./cmd/mcp-relayd
golangci-lint fmt --diff ./...
golangci-lint run ./...
typos --force-exclude .
go test ./...
go build -o bin/mcp-relayd ./cmd/mcp-relayd
```

Los hooks usan los comandos del entorno devenv; usa `devenv shell` cuando
vayas a instalar o ejecutar los hooks.
