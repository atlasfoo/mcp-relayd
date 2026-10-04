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
Go, gopls y Git mediante Nix y genera `devenv.lock`. Guarda ese archivo en el
repositorio para que otros desarrolladores usen las mismas revisiones.

Dentro del entorno:

```sh
check  # formato, go vet y go test
build  # genera bin/mcp-relayd
```

También puedes ejecutar la comprobación del entorno desde fuera del shell:

```sh
devenv test
```

## Estructura

```text
cmd/mcp-relayd/main.go  # punto de entrada
go.mod                 # módulo Go
devenv.nix             # herramientas y comandos
devenv.yaml            # origen de los paquetes Nix
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

Si prefieres tu propia instalación de Go (1.25 o posterior):

```sh
go run ./cmd/mcp-relayd
go vet ./...
go test ./...
go build -o bin/mcp-relayd ./cmd/mcp-relayd
```
