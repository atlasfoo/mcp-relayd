# mcp-relayd

## Objetivo

`mcp-relayd` será un daemon local multiplataforma encargado de mantener instancias persistentes de servidores MCP que originalmente funcionan mediante `stdio`, y exponerlas mediante **MCP Streamable HTTP** para que múltiples agentes y herramientas puedan reutilizar las mismas instancias.

El problema que busca resolver es la duplicación de procesos MCP cuando varios clientes —por ejemplo Codex, Claude, OpenCode, Cursor, Zed/ACP o JetBrains— ejecutan simultáneamente servidores MCP locales mediante `npx`, `uvx` o binarios standalone.

En lugar de:

```text
Codex ───────► MCP A
Claude ──────► MCP A
OpenCode ────► MCP A
Zed/ACP ─────► MCP A
```

donde cada cliente levanta su propio proceso, se busca:

```text
Codex ───────┐
Claude ──────┤
OpenCode ────┼──► mcp-relayd ───► MCP A
Zed/ACP ─────┘
```

De esta forma existe una única instancia persistente de cada MCP compartible.

---

## Principios de diseño

La herramienta debe priorizar:

- bajo consumo de recursos;
- startup rápido;
- distribución como binario standalone;
- compatibilidad con Linux, macOS y Windows;
- configuración declarativa;
- administración sencilla;
- aislamiento entre diferentes servidores MCP;
- compatibilidad con clientes MCP estándar;
- exposición únicamente local por defecto;
- ausencia de dependencia de tmux, Node o Python para el runtime del gateway.

El stack principal propuesto es **Go**.

---

# Arquitectura general

```text
                      localhost
                  Streamable HTTP
                         │
         ┌───────────────┼───────────────┐
         │               │               │
       Codex           Claude         OpenCode
         │               │               │
         └───────────────┼───────────────┘
                         │
                         ▼
               ┌──────────────────┐
               │    mcp-relayd    │
               │                  │
               │ HTTP Router      │
               │ MCP Sessions     │
               │ Process Manager  │
               │ Config Manager   │
               │ Logging          │
               └────────┬─────────┘
                        │
              stdio     │
         ┌──────────────┼──────────────┐
         ▼              ▼              ▼
      MCP A           MCP B          MCP C

      uvx ...         npx ...        binary
```

Cada MCP configurado debe mantener su identidad individual.

Por ejemplo:

```text
http://127.0.0.1:9876/mcp/context7
http://127.0.0.1:9876/mcp/fetch
http://127.0.0.1:9876/mcp/github
```

No se busca inicialmente agregar todas las tools de todos los MCP en un único servidor lógico.

La relación debería mantenerse como:

```text
1 MCP configurado
=
1 proceso upstream
=
1 endpoint Streamable HTTP
```

---

# Configuración

La configuración puede utilizar TOML.

Ejemplo:

```toml
[gateway]
host = "127.0.0.1"
port = 9876

[servers.context7]
command = "npx"
args = ["-y", "@upstash/context7-mcp"]
autostart = true

[servers.fetch]
command = "uvx"
args = ["mcp-server-fetch"]
autostart = true

[servers.custom]
command = "/home/user/.local/bin/custom-mcp"
autostart = false

[servers.custom.env]
API_KEY = "${CUSTOM_API_KEY}"
```

Cada definición de servidor debería eventualmente poder soportar:

```text
command
args
env
cwd
autostart
restart policy
startup timeout
shutdown timeout
idle timeout
```

La configuración debe ser independiente de las configuraciones propias de Codex, Claude, OpenCode, etc.

Los clientes sólo conocerán el endpoint HTTP.

---

# Componentes principales

## Process Manager

Responsable de administrar los servidores MCP upstream.

Debe poder:

```text
spawn
stop
restart
kill
detectar exit
capturar stderr
mantener stdin/stdout
```

Cada proceso debe tener un lifecycle explícito:

```text
STOPPED
   │
   ▼
STARTING
   │
   ▼
INITIALIZING
   │
   ▼
READY
   │
   ├──► FAILED
   │
   └──► STOPPING
             │
             ▼
          STOPPED
```

---

## MCP Transport Layer

Responsable de traducir:

```text
Streamable HTTP
       ↕
MCP / JSON-RPC
       ↕
stdio
```

La implementación debe permitir que múltiples clientes HTTP utilicen una única instancia stdio cuando el MCP sea compatible con ese modelo.

Debe prestarse especial atención a funcionalidades MCP que introducen estado o comunicación server→client, como:

```text
sampling
elicitation
roots
notifications
logging
client capabilities
```

La primera versión puede limitar explícitamente algunas de estas funcionalidades.

---

## HTTP Router

Debe exponer endpoints similares a:

```text
/mcp/{server}
```

Ejemplo:

```text
POST /mcp/context7
POST /mcp/fetch
POST /mcp/github
```

También puede existir una API administrativa separada:

```text
/api/servers
/api/servers/{name}
/health
```

La interfaz MCP y la API administrativa deben permanecer conceptualmente separadas.

---

## Config Manager

Responsable de:

```text
leer configuración
validarla
resolver variables de entorno
crear definiciones de procesos
```

Posteriormente puede incorporar hot reload.

---

## Observabilidad

Desde las primeras versiones deberían existir logs estructurados.

Ejemplo:

```text
server=context7
event=started
pid=18432
```

o:

```text
server=context7
event=exited
code=1
```

En versiones posteriores se pueden incorporar:

```text
uptime
cantidad de clientes
cantidad de requests
CPU
RAM
restart count
last error
```

---

# CLI

La herramienta puede evolucionar hacia una interfaz similar a:

```bash
mcp-relayd run
mcp-relayd status
mcp-relayd list

mcp-relayd start context7
mcp-relayd stop context7
mcp-relayd restart context7

mcp-relayd logs context7
mcp-relayd logs -f context7
```

No es necesario separar inicialmente daemon y CLI en dos binarios.

Puede existir un único ejecutable:

```text
mcp-relayd
```

con diferentes subcomandos.

---

# Integración con el sistema operativo

`mcp-relayd` debe funcionar como daemon persistente.

El sistema operativo debe supervisar únicamente al gateway:

```text
OS supervisor
     │
     ▼
 mcp-relayd
     │
     ├── MCP A
     ├── MCP B
     └── MCP C
```

Posteriormente pueden incorporarse integraciones nativas:

```text
Linux    → systemd --user
macOS    → launchd
Windows  → Windows Service
```

El gateway administra sus procesos hijos; el sistema operativo administra el gateway.

---

# Seguridad

Por defecto el servidor debe escuchar únicamente en:

```text
127.0.0.1
```

y no:

```text
0.0.0.0
```

Los MCP pueden tener acceso a:

```text
filesystem
tokens
cloud credentials
databases
shell
browser automation
APIs
```

por lo que transformar un servidor local `stdio` en HTTP no debe convertirlo accidentalmente en un servicio accesible desde la LAN.

La exposición remota debe ser una decisión explícita.

---

# Scope inicial

`mcp-relayd` debe enfocarse inicialmente en MCPs que no dependan fuertemente del contexto particular de un proyecto.

Buenos candidatos:

```text
Context7
fetch
GitHub
documentation servers
web/search services
cloud APIs
shared databases
general-purpose integrations
```

Servidores dependientes del proyecto requieren mayor análisis:

```text
filesystem
git
language servers
project databases
build tooling
```

Estos pueden depender de:

```text
cwd
roots
environment
workspace
estado específico del cliente
```

y no deberían ser el foco inicial.

---

# Fases del proyecto

## Fase 0 — Proof of Concept

Objetivo: validar la hipótesis principal.

Construir una prueba sencilla utilizando herramientas existentes.

```text
mcp-proxy
+
scripts
+
opcionalmente tmux
```

Validar:

```text
varios clientes
        │
        ▼
Streamable HTTP
        │
        ▼
una sola instancia stdio
```

Comprobar específicamente:

- que varios agentes pueden conectarse simultáneamente;
- que realmente existe un solo proceso MCP;
- comportamiento al conectar/desconectar clientes;
- compatibilidad con MCPs reales;
- reducción de startup overhead;
- reducción de procesos y memoria.

`tmux` puede ser útil aquí para debugging, pero no debe convertirse en dependencia arquitectónica.

---

## Fase 1 — Core daemon

Crear el primer `mcp-relayd` en Go.

Scope:

```text
config TOML
process manager
multiple MCP definitions
stdio process handling
HTTP server
basic logging
graceful shutdown
```

Inicialmente puede delegarse la conversión:

```text
stdio ↔ Streamable HTTP
```

a `mcp-proxy`.

Arquitectura temporal:

```text
mcp-relayd
   │
   ├── mcp-proxy → MCP A
   ├── mcp-proxy → MCP B
   └── mcp-proxy → MCP C
```

Esto permite desarrollar primero la identidad del producto:

```text
configuration
lifecycle
supervision
routing
management
```

sin implementar todavía todo MCP.

---

## Fase 2 — Native MCP bridge

Eliminar la dependencia de `mcp-proxy`.

Implementar dentro de `mcp-relayd`:

```text
Streamable HTTP
       ↕
MCP session routing
       ↕
stdio
```

Arquitectura resultante:

```text
mcp-relayd
   │
   ├── MCP A
   ├── MCP B
   └── MCP C
```

Objetivo:

```text
1 gateway
+
1 proceso por MCP
```

sin procesos proxy adicionales.

Esta es la fase técnicamente más importante.

---

## Fase 3 — Process supervision

Agregar robustez al lifecycle.

Funciones:

```text
restart on failure
restart always
restart never
startup timeout
graceful shutdown
forced shutdown
restart backoff
crash detection
health state
```

Cada servidor debe poder fallar de forma independiente sin afectar al gateway completo.

---

## Fase 4 — CLI administrativa

Incorporar administración cómoda.

Ejemplos:

```text
list
status
start
stop
restart
logs
logs --follow
reload
```

Ejemplo:

```text
$ mcp-relayd status

NAME       STATUS   PID     CLIENTS
context7   ready    18342   4
fetch      ready    18351   2
github     stopped  -       0
```

---

## Fase 5 — Lazy lifecycle

Permitir que un servidor no tenga que estar permanentemente ejecutándose.

Configuración futura:

```toml
[servers.playwright]
command = "npx"
args = ["..."]
start = "lazy"
idle_timeout = "10m"
```

Funcionamiento:

```text
HTTP request
     │
     ▼
server stopped
     │
     ▼
spawn
     │
     ▼
initialize
     │
     ▼
forward request
```

Después de un período sin clientes:

```text
idle timeout
     │
     ▼
shutdown
```

Esto permite mantener el beneficio de procesos persistentes sin tener todos los MCP permanentemente consumiendo recursos.

---

## Fase 6 — Observabilidad

Agregar métricas y diagnóstico.

Ejemplo:

```text
SERVER       CPU     RAM      CLIENTS   CALLS
context7     0.2%    82 MB    4         157
fetch        0.0%    41 MB    2          32
playwright   3.2%   312 MB    1          18
```

Posibles comandos:

```bash
mcp-relayd stats
mcp-relayd top
```

También:

```text
restart count
uptime
last error
requests
latency
```

---

## Fase 7 — Integración con OS

Agregar mecanismos de instalación como servicio.

Linux:

```text
systemd --user
```

macOS:

```text
launchd
```

Windows:

```text
Windows Service
```

La experiencia final debería aproximarse a:

```bash
mcp-relayd install
mcp-relayd start
```

y posteriormente ejecutarse automáticamente con la sesión del usuario.

---

## Fase 8 — Distribución y hardening

Preparar el proyecto para terceros.

Incluye:

```text
cross compilation
GitHub releases
checksums
installation scripts
CI/CD
tests
documentation
example configs
migration docs
compatibility matrix
```

Targets principales:

```text
linux-amd64
linux-arm64
darwin-amd64
darwin-arm64
windows-amd64
windows-arm64
```

---

# Fuera del scope inicial

Evitar inicialmente:

```text
Web UI
tool aggregation
tool namespacing
remote gateway
multi-user support
complex authentication
plugin system
service discovery
distributed gateways
automatic MCP installation
filesystem/project context management
```

Estas funcionalidades pueden incorporarse posteriormente, pero no son necesarias para resolver el problema original.

---

# Definición de éxito de la primera versión

Una primera versión funcional debe permitir configurar:

```toml
[servers.context7]
command = "npx"
args = ["-y", "@upstash/context7-mcp"]
```

ejecutar:

```bash
mcp-relayd run
```

y configurar múltiples agentes para utilizar:

```text
http://127.0.0.1:9876/mcp/context7
```

de manera que:

```text
Codex
Claude
OpenCode
Zed/ACP
```

puedan utilizar simultáneamente **una única instancia persistente de Context7**.

Ese escenario constituye el principal criterio de aceptación de `mcp-relayd`.

---

# Visión del producto

La propuesta puede resumirse en:

> **Configure your local MCP servers once, keep them alive, and share them across every agent.**

Conceptualmente, `mcp-relayd` no debe convertirse simplemente en otro MCP proxy.

Su responsabilidad principal es:

```text
centralizar
+
supervisar
+
reutilizar
```

la infraestructura MCP local.

El resultado esperado es convertir:

```text
Agent-owned MCP processes
```

en:

```text
User-owned MCP infrastructure
```

desacoplando completamente el lifecycle de los servidores MCP del lifecycle de cada agente.
