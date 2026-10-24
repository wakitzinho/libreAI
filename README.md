# libreAI

libreAI is a lightweight, self-hosted AI chat application for a personal Linux
server. It ships a Go backend, an embedded vanilla HTML/CSS/JavaScript
interface, and SQLite storage in one static binary. There is no Node.js
runtime, no frontend build step, no Redis, no local model inference, and no
user-account system.

It is designed for the **ThinkPad X301** (Intel Core 2 Duo SU9400, 1.4 GHz) as
well as any modern `linux/amd64` or `linux/arm64` machine. The intended access
path is a private Tailscale tailnet, not the public Internet.

## Features

- OpenAI-compatible providers configured server-side in `.env`; API keys never
  reach the browser.
- Streaming chat over SSE, with **Stop**, **Regenerate**, and **Edit & resend**.
- Markdown and code-block rendering with an allowlist sanitizer, syntax
  highlighting, and copy buttons.
- Projects, chat search, move/rename/delete, and Markdown/JSON export.
- Automatic deletion of chats inactive for 30 days (configurable), at startup
  and every six hours.
- Catppuccin Mocha/Latte themes, system theme support, responsive mobile
  drawer, and accessible keyboard operation.
- SQLite in WAL mode with foreign keys, migrations, bounded connections,
  partial-response persistence, and interrupted-generation recovery.
- Static `linux/amd64` binary built at `GOAMD64=v1` with `CGO_ENABLED=0`.
- Native systemd/NixOS deployment and an optional distroless Docker image.

## Compatibility status

This repository was prepared on a modern x86-64 Linux development machine.
The following was checked locally:

| Check | Result |
| --- | --- |
| `GOAMD64=v1`, `CGO_ENABLED=0` build | Verified |
| Static ELF, no dynamic libraries | Verified (`ldd`: not a dynamic executable) |
| Application/module code AVX/AVX2/AVX-512/FMA scan | No matches |
| Complete-binary SIMD scan | AVX/FMA only in CPUID-dispatched Go runtime/stdlib paths |
| Startup with optional CPU features disabled (`GODEBUG=cpu.all=off`) | Verified locally (health endpoint, clean shutdown) |
| `go test ./...` and frontend tests | Verified locally |
| Server starts, serves assets, mock-provider streaming | Verified locally |
| Desktop UI smoke test in headless Firefox | Verified locally (models, streaming, Markdown, sidebar) |
| Persistence across restart, retention cleanup, interrupted recovery | Verified locally with a mock provider |
| systemd unit directives | Validated with `systemd-analyze verify` (placeholder `ExecStart`) |
| **Actual systemd service activation** | **Not performed in the preparation environment** |
| **Runtime on an actual ThinkPad X301** | **Not verified — no X301 was available** |
| Docker build/run | **Not verified — Docker was not available** |
| Nix build/NixOS module evaluation | **Not verified — Nix was not available** |

Local checks were run on 2026-10-24 with Go 1.27.1.

A successful build on a modern machine is **not** proof of X301 compatibility.
See [Building for the X301](#building-for-the-x301) for the verification
procedure. Do not claim X301 support until that procedure has been run on the
hardware.

## Requirements

- Linux on `x86_64` (or another Linux architecture if you build natively).
- Go **1.26 or newer** to build (`go.mod` declares `go 1.26.0`; development used
  Go 1.27.1). A C compiler is not required.
- `make`, `git`, and `curl` for the documented commands.
- Optional: Node.js 20+ only for the development frontend tests.
- Optional: `sqlite3` for online backups.
- Optional: Nix with flakes, or Docker with Compose, for those deployment
  methods.

The server needs outbound HTTPS access to your provider. It does not need
inbound access except from the local browser or Tailscale Serve.

## Quick start

```sh
git clone https://github.com/wakitzinho/libreAI.git libreAI
cd libreAI

cp .env.example .env
chmod 600 .env
# Edit .env: enable at least one provider and add its API key.

make test
make build
./bin/libreai
```

Open <http://127.0.0.1:8080>. The database is created at
`./data/libreai.db` by default.

To use a different configuration file:

```sh
./bin/libreai -env /path/to/libreai.env
```

`ENV_FILE` sets the default path if `-env` is not given. Other flags:

```sh
./bin/libreai -version
./bin/libreai -healthcheck -env /etc/libreai/libreai.env
```

The health check requests `http://127.0.0.1:<PORT>/api/health` (or the loopback
equivalent of the configured `HOST`) and exits non-zero when unhealthy.

## Configuration

Configuration is read from the `.env` file and then overlaid with the process
environment. **Process environment variables always win**, which is useful for
systemd and containers. A missing `.env` file is not fatal; defaults apply.

Keep the file private (`chmod 600`) and outside Git. `make` never copies it
into the binary.

### Core settings

| Variable | Default | Meaning |
| --- | --- | --- |
| `HOST` | `127.0.0.1` | Bind address. Keep loopback unless another layer protects access. |
| `PORT` | `8080` | HTTP port (1–65535). |
| `DATABASE_PATH` | `./data/libreai.db` | SQLite file. Relative paths use the working directory. |
| `CHAT_RETENTION_DAYS` | `30` | Delete chats inactive for this many days (1–3650). |
| `UPSTREAM_TIMEOUT_SECONDS` | `300` | Default provider request timeout (10–3600). |
| `MAX_CONCURRENT_GENERATIONS` | `2` | Global concurrent streams (1–16). Keep small on old hardware. |
| `MAX_REQUEST_BYTES` | `1048576` | Browser JSON body limit (1 KiB–10 MiB). |
| `MAX_CONTEXT_MESSAGES` | `200` | Messages included in provider context (1–1000). |
| `MAX_CONTEXT_BYTES` | `400000` | Approximate context byte limit (1 KiB–4 MiB). |
| `SHUTDOWN_TIMEOUT_SECONDS` | `15` | Graceful shutdown window (1–120). |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error`. |
| `ENV_FILE` | `.env` | Default config path when `-env` is omitted. |

### Providers

Each provider is named by an ID and uses variables of the form
`PROVIDER_<ID>_<FIELD>`. IDs start with a lowercase letter and contain only
`a-z`, `0-9`, and `_` (maximum 32 characters). Field names:

| Field | Default | Notes |
| --- | --- | --- |
| `ENABLED` | `false` | `true`/`false`, `yes`/`no`, `on`/`off`, or `1`/`0`. |
| `DISPLAY_NAME` | Capitalized ID | Label shown in the UI (maximum 80 characters). |
| `BASE_URL` | — | Required when enabled; `http` or `https`, usually ending in `/v1`. Query strings, fragments, and embedded credentials are rejected. |
| `API_KEY` | — | Server-side secret, sent as `Authorization: Bearer …`. |
| `NO_AUTH` | `false` | Set `true` for a local server that needs no key. |
| `MODELS` | — | Comma-separated manual model IDs, merged with discovery. |
| `DISCOVERY` | `true` | Call `GET {BASE_URL}/models` and merge the results. |
| `TIMEOUT_SECONDS` | global | Per-provider timeout (5–3600). |
| `HEADERS` | `{}` | JSON object of extra request headers, e.g. `{"X-Team":"dev"}`. |

libreAI uses these OpenAI-compatible endpoints:

```text
POST {BASE_URL}/chat/completions
GET  {BASE_URL}/models              (when DISCOVERY=true)
```

Example for OpenAI:

```dotenv
PROVIDER_OPENAI_ENABLED=true
PROVIDER_OPENAI_DISPLAY_NAME=OpenAI
PROVIDER_OPENAI_BASE_URL=https://api.openai.com/v1
PROVIDER_OPENAI_API_KEY=sk-...
PROVIDER_OPENAI_MODELS=gpt-4.1-mini
```

Example for a local OpenAI-compatible server with no key:

```dotenv
PROVIDER_LOCAL_ENABLED=true
PROVIDER_LOCAL_DISPLAY_NAME=Local
PROVIDER_LOCAL_BASE_URL=http://127.0.0.1:11434/v1
PROVIDER_LOCAL_NO_AUTH=true
PROVIDER_LOCAL_MODELS=local-model
PROVIDER_LOCAL_DISCOVERY=false
```

If an enabled provider has no key and `NO_AUTH` is not `true`, or has no models
while discovery is disabled, startup logs a warning and the provider is shown as
unavailable. An enabled provider with a missing or invalid `BASE_URL`, or an
invalid provider ID, numeric field, boolean, or headers value, stops startup with
a clear message.

API keys and custom header values are never returned by `/api/models` or
`/api/bootstrap`, and `Provider.String()`/slog output omit them. Known keys are
redacted from provider errors before logging.

### Model discovery and manual fallback

Discovery is best-effort and never blocks startup. At startup, libreAI calls
`/models` once for each enabled provider whose `DISCOVERY` is true and caches
the result in memory. The **Settings → Refresh models** button performs an
explicit refresh. A failed refresh keeps the previous successful list and all
manual `MODELS`.

Manual model IDs always remain selectable even when discovery fails. If a
provider has no manual models and discovery has never succeeded, the provider
is visible but generation is rejected with a clear error. Add `MODELS` or fix
discovery.

The settings dialog stores the default provider/model and hidden models in the
database. Theme and sidebar preferences are browser-local (`localStorage`).

## Using libreAI

- **New chat** starts an unsaved chat; the first message creates/saves it.
- **Enter** sends, **Shift+Enter** inserts a newline, and **Stop** cancels the
  upstream request while preserving partial text.
- **Copy** copies a message; code blocks have their own copy button.
- **Edit & resend** rewrites a user message and discards later messages before
  regenerating. **Regenerate** replaces the last assistant response.
- **Projects** group chats. Use the project menu to rename/delete; deleting a
  project moves its chats to the general list. Move chats from the chat menu.
- **Export** downloads the current chat as Markdown or JSON.
- **Themes**: system, light (Catppuccin Latte), or dark (Catppuccin Mocha).
- On small screens the sidebar becomes a drawer; focus management and Escape
  closing are supported.

### Chat retention

A chat's `last_activity_at` changes when the chat is opened in the UI or a
message is sent—not when a list or `GET` request merely reads it. Cleanup runs
immediately at startup (so downtime is handled) and every six hours. Chats
inactive for `CHAT_RETENTION_DAYS` are deleted with their messages. The Settings
dialog and empty state show the configured period.

## Storage, backup, and restore

The live data is `DATABASE_PATH` plus SQLite's `-wal` and `-shm` files in the
same directory. The directory is created with mode `0700` and the database with
mode `0600`. Do not run two libreAI processes against the same database.

### Online backup

If `sqlite3` is installed, use SQLite's backup command while the service runs:

```sh
sudo -u libreai sqlite3 /var/lib/libreai/libreai.db \
  ".backup '/var/backups/libreai-$(date +%F-%H%M).db'"
```

### Offline backup

```sh
sudo systemctl stop libreai
sudo install -d -m 0700 /var/backups/libreai
sudo cp -a /var/lib/libreai/libreai.db* /var/backups/libreai/
sudo systemctl start libreai
```

Copy the Markdown/JSON exports too if you want a human-readable archive.

### Restore

```sh
sudo systemctl stop libreai
sudo install -m 0600 -o libreai -g libreai \
  /var/backups/libreai/libreai.db /var/lib/libreai/libreai.db
sudo rm -f /var/lib/libreai/libreai.db-wal /var/lib/libreai/libreai.db-shm
sudo systemctl start libreai
```

For a user-run server, stop the process, copy the database back, and restart.
A backup contains all chats and must be protected accordingly.

## Testing

```sh
make test        # Go tests plus optional Node frontend tests
make test-go     # go test ./...
make test-js     # node --test tests/frontend/*.test.mjs (skipped without Node)
make vet         # go vet ./...
```

The Go tests cover configuration validation and redaction, provider request
formatting, SSE parsing and errors, cancellation, model discovery, store CRUD,
foreign keys, retention, interrupted-generation recovery, concurrency, export,
security headers/origin checks, and end-to-end HTTP streaming against mock
providers. They require no paid API calls or network access.

The frontend tests exercise the Markdown sanitizer/XSS cases, SSE parser, and
renderer helpers under Node's built-in test runner. Node is a development-only
dependency; the server neither embeds nor requires it. A desktop UI smoke test
was also run in headless Firefox against the mock provider, covering model
population, message sending and SSE streaming, Markdown rendering, and sidebar
navigation. A real mobile device was not available, so mobile layout and touch
behavior were not tested on hardware.

## Building for the X301

The target CPU is x86-64-v1 (SSE2 baseline). Build with:

```sh
make build-linux-amd64
# produces bin/libreai-linux-amd64

make check-binary
# or inspect the explicit output:
file bin/libreai-linux-amd64
ldd bin/libreai-linux-amd64          # expected: not a dynamic executable
go version -m bin/libreai-linux-amd64
```

Expected embedded settings include:

```text
CGO_ENABLED=0
GOARCH=amd64
GOOS=linux
GOAMD64=v1
```

`make check-binary` builds an unstripped copy for disassembly (the release
binary is stripped and cannot be inspected with `go tool objdump`), then checks
the mnemonic field rather than raw symbol names. It fails if the application,
`main` package, or any bundled third-party module contains AVX, AVX2, AVX-512,
or FMA instructions.

`GOAMD64=v1` controls the code the Go compiler generates. The Go runtime and
standard library still ship hand-written assembly with optional AVX/AVX2/
AVX-512/FMA implementations. Those paths are selected at run time through
CPUID, so an x86-64-v1 CPU never executes them; `make check-binary` reports
their presence separately instead of treating them as failures. On the
development machine, `GODEBUG=cpu.all=off` can force the non-SIMD fallbacks to
exercise that path.

The pure-Go `modernc.org/sqlite` driver means there is no CGo SQLite library and
no dynamic libc dependency.

### Actual X301 verification procedure

1. Build with `make build-linux-amd64` and confirm the checks above.
2. Copy the binary and a test `.env` to the X301:
   ```sh
   scp bin/libreai-linux-amd64 x301:/tmp/libreai
   scp .env x301:/tmp/libreai.env
   ```
3. On the X301:
   ```sh
   chmod +x /tmp/libreai
   /tmp/libreai -version
   /tmp/libreai -env /tmp/libreai.env
   ```
   In another terminal:
   ```sh
   curl -fsS http://127.0.0.1:8080/api/health
   curl -fsS http://127.0.0.1:8080/ | head
   ```
4. Open the UI through an SSH tunnel or Tailscale, send a short prompt, stop a
   generation, and restart the service to confirm persistence.
5. Watch for `Illegal instruction`, crashes, or OOM kills in the journal.
6. Record the CPU model (`grep 'model name' /proc/cpuinfo`), Go version, and
   results. Until this is done, report X301 runtime compatibility as
   **unverified**.

## Native systemd installation

The supplied unit runs as the dedicated `libreai` user, reads
`/etc/libreai/libreai.env`, and stores data in `/var/lib/libreai`.

```sh
make build

sudo install -Dm755 bin/libreai /usr/local/bin/libreai
sudo install -Dm644 deploy/systemd/libreai.service /etc/systemd/system/libreai.service
sudo install -Dm644 deploy/systemd/libreai.sysusers /etc/sysusers.d/libreai.conf
sudo install -Dm644 README.md /usr/local/share/doc/libreai/README.md
sudo install -Dm644 SECURITY.md /usr/local/share/doc/libreai/SECURITY.md
sudo install -Dm644 LICENSE /usr/local/share/doc/libreai/LICENSE

sudo systemd-sysusers
sudo install -d -m 0750 -o root -g libreai /etc/libreai
sudo install -m 0640 -o root -g libreai .env /etc/libreai/libreai.env

sudo systemctl daemon-reload
sudo systemctl enable --now libreai
sudo systemctl status libreai
journalctl -u libreai -f
```

Check the endpoint:

```sh
curl -fsS http://127.0.0.1:8080/api/health
```

The unit uses `StateDirectory=libreai` (creating `/var/lib/libreai` with mode
`0700`), `Restart=on-failure`, a journald identifier, and systemd hardening:
no new privileges, no capabilities, strict system protection, private
`/tmp` and devices, an empty capability bounding set, restricted address
families/namespaces, and `@system-service` syscall filtering. The only
persistent writable path is `/var/lib/libreai`; `PrivateTmp=true` provides an
ephemeral private `/tmp`.

If startup fails, check that `/etc/libreai/libreai.env` is readable by the
service (`root:libreai`, mode `0640`), that the provider URL is reachable, and
that `/var/lib/libreai` is writable. The unit does not need root, a desktop, or
any interpreter.

### Updating and rollback (native)

```sh
# Optional: keep the current binary for rollback.
sudo cp /usr/local/bin/libreai /usr/local/bin/libreai.previous

git pull
make test
make build
sudo systemctl stop libreai
sudo install -m 0755 bin/libreai /usr/local/bin/libreai
sudo systemctl start libreai
```

The database and `.env` are untouched. Database migrations are forward-only;
if a new version migrated the schema, restore a pre-update database backup when
rolling back to an older binary.

## NixOS and the flake

The repository provides:

- `deploy/nixos/package.nix` — `buildGoModule` package at `GOAMD64=v1`.
- `deploy/nixos/module.nix` — `services.libreai` NixOS module.
- `flake.nix` — `packages.<system>.libreai`, `apps`, and `nixosModules.default`.

Nix was **not available** while preparing this repository, so the flake and
module have not been evaluated here. The package intentionally uses
`vendorHash = lib.fakeHash`; complete it with one Nix build as follows.

### One-time vendor hash

Flakes only see files known to Git. Add the repository first:

```sh
cd /srv/libreai
git add .
git commit -m "Initial libreAI"
nix build .#libreai
```

The build stops with a hash mismatch and prints the expected `sha256-...`.
Replace `vendorHash = lib.fakeHash;` in `deploy/nixos/package.nix` with that
value, then:

```sh
nix build .#libreai
./result/bin/libreai -version
```

The hash changes only when `go.mod`/`go.sum` dependencies change.

### NixOS configuration

In your system flake:

```nix
{
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  inputs.libreai.url = "path:/srv/libreai";

  outputs = { nixpkgs, libreai, ... }: {
    nixosConfigurations.myhost = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        ./configuration.nix
        libreai.nixosModules.default
        {
          services.libreai = {
            enable = true;
            # Defaults: user/group libreai, dataDir /var/lib/libreai,
            # host 127.0.0.1, port 8080, environmentFile below.
            environmentFile = "/etc/libreai/libreai.env";
            environment.CHAT_RETENTION_DAYS = 30; # non-secret settings only
          };
        }
      ];
    };
  };
}
```

Deploy once, then create the credentials file (the `libreai` group exists after
activation) and restart:

```sh
sudo nixos-rebuild switch --flake /etc/nixos#myhost

sudo install -d -m 0750 -o root -g libreai /etc/libreai
sudo install -m 0640 -o root -g libreai .env /etc/libreai/libreai.env
sudo systemctl restart libreai

systemctl status libreai
journalctl -u libreai -f
```

Do **not** put API keys in the Nix store or `services.libreai.environment`;
store values are world-readable. Use `environmentFile` managed by your secrets
tool (sops-nix, agenix, or a manually installed file).

Module options: `enable`, `package`, `user`, `group`, `dataDir`, `host`,
`port`, `environmentFile`, `environment`, and `openFirewall`. The module creates
the system user/group, the data directory (`0700`), and a hardened systemd
service analogous to the standalone unit. Keep `host = "127.0.0.1"` and use
Tailscale Serve; only set `openFirewall = true` if you intentionally bind a
non-loopback address behind another access-control layer.

The default data directory persists across `nixos-rebuild switch` and binary
updates. To update, update the flake input and rebuild:

```sh
cd /etc/nixos
nix flake update libreai nixpkgs
sudo nixos-rebuild switch --flake .#myhost
```

## Optional Docker deployment

Docker is an alternative, not the smallest deployment. The image is a static
Go binary in `gcr.io/distroless/static-debian12:nonroot`; it contains no shell,
package manager, Node.js, or SQLite server. The Dockerfile and Compose service
are fixed to `linux/amd64`, built with `GOAMD64=v1`; building on an ARM host
therefore requires amd64 emulation.

```sh
cp .env.example .env
chmod 600 .env
# Edit .env with providers. HOST/PORT/DATABASE_PATH are overridden below.
docker compose build
docker compose up -d
docker compose logs -f libreai
curl -fsS http://127.0.0.1:8080/api/health
```

The container listens on `0.0.0.0:8080` internally, but Compose publishes only
`127.0.0.1:8080`. The database lives in the named volume `libreai-data`, mounted
at `/data`. The container runs as UID/GID 65532, drops all capabilities, uses
`no-new-privileges`, has a read-only root filesystem, and uses a small tmpfs.

Upgrade:

```sh
git pull
docker compose build
docker compose up -d
```

Data in the volume is preserved. Roll back by checking out the previous version
and rebuilding; restore a database backup if the newer version migrated the
schema.

### Docker backup and restore

The runtime image has no shell, so use a helper container. Find the actual
volume name first:

```sh
docker volume ls --filter label=com.docker.compose.volume=libreai-data
```

Stop the service for a consistent copy (the example uses the default project
volume name):

```sh
docker compose stop
mkdir -p backups
docker run --rm \
  -v libreai_libreai-data:/data:ro \
  -v "$PWD/backups:/backup" \
  alpine:3 sh -c 'cp /data/libreai.db* /backup/'
docker compose start
```

Restore:

```sh
docker compose stop
docker run --rm \
  -v libreai_libreai-data:/data \
  -v "$PWD/backups:/backup" \
  alpine:3 sh -c 'rm -f /data/libreai.db*; cp /backup/libreai.db /data/libreai.db; chown 65532:65532 /data/libreai.db'
docker compose start
```

Protect backups like the live database. The Compose file never mounts the
Docker socket or the host filesystem, does not use host networking, and does
not run privileged.

## Access through Tailscale

Keep libreAI on `127.0.0.1` and put Tailscale Serve in front of it:

```sh
sudo tailscale serve --bg http://127.0.0.1:8080
tailscale serve status
```

Open the printed `https://<machine>.<tailnet>.ts.net` URL. Remove the proxy
with:

```sh
sudo tailscale serve reset
```

Tailscale provides transport security and tailnet identity. **Every tailnet
user allowed by your ACLs can use libreAI, read all chats, and spend your
provider quota.** Restrict access with tailnet ACLs and device approval. Do not
enable public funnel access unless you add a separate authentication layer.
See [SECURITY.md](SECURITY.md) for the full trust model.

## Troubleshooting

| Symptom | Things to check |
| --- | --- |
| UI loads but says no provider is usable | Set `PROVIDER_<ID>_ENABLED=true`, a valid `BASE_URL`, and an `API_KEY` or `NO_AUTH=true`. Restart after editing `.env`. |
| Provider returns 401/403 | Wrong/revoked key, wrong account, or the key is not authorized for the selected model. Fix the server-side key and restart. |
| “Model is not currently known” | Discovery failed or returned no models. Add the exact ID to `PROVIDER_<ID>_MODELS`, fix discovery, or use **Refresh models**. |
| Discovery fails | Check `BASE_URL`, outbound network, key permissions, and whether the server implements `GET /models`. Manual `MODELS` still work. |
| 429 / rate limit | Wait, check the provider dashboard, or configure another provider. libreAI does not retry automatically. |
| “Too many responses are being generated” | `MAX_CONCURRENT_GENERATIONS` was reached. Stop another generation or raise the limit modestly. |
| “A response is already being generated for this chat” | Only one stream per chat is allowed. Stop the existing one or open another chat. |
| Startup error about `DATABASE_PATH`/permissions | Ensure the directory exists or can be created, and the service user owns it. For systemd, it is `/var/lib/libreai`. |
| “database is locked” | Do not run two instances against one database. Check that WAL files are writable and the filesystem is local (not a fragile network mount). |
| Port already in use | `ss -ltnp \| grep 8080`, then stop the conflicting process or change `PORT`. |
| Systemd starts then exits | `journalctl -u libreai -e`; verify `/etc/libreai/libreai.env` ownership/mode and that the provider URL is valid. |
| Health check fails | `curl -v http://127.0.0.1:8080/api/health`; if `HOST` is `0.0.0.0`, the built-in check uses `127.0.0.1`. |
| Docker health check fails | `docker compose ps`, `docker compose logs libreai`; confirm `.env` exists and the volume is writable. |
| Chats disappeared | Retention deletes chats after `CHAT_RETENTION_DAYS`; opening a chat or sending a message refreshes activity. Restore a backup if needed. |
| Markdown looks like plain text | Code fences/links are rendered through the safe allowlist; raw HTML is intentionally escaped. |

Useful commands:

```sh
./bin/libreai -version
./bin/libreai -healthcheck -env .env
systemctl status libreai
journalctl -u libreai --since '1 hour ago'
```

The server logs request method/path, status, duration, and byte count, but not
request bodies, query strings, headers, prompts, or API keys.

## Project layout

```text
cmd/libreai/            server entrypoint, flags, shutdown, health check
internal/config/        .env parsing, validation, provider configuration
internal/provider/      OpenAI-compatible client, SSE decoder, discovery
internal/store/         SQLite schema, migrations, CRUD, recovery
internal/retention/     periodic 30-day chat cleanup
internal/httpserver/    JSON API, SSE endpoint, static assets, security
web/                    embedded HTML/CSS/ES-module frontend
tests/frontend/         development-only Node tests
deploy/systemd/         native service unit and sysusers file
deploy/nixos/           Nix package and NixOS module
Dockerfile, compose.yaml optional container deployment
```

## License

MIT. See [LICENSE](LICENSE).
