# Security policy and threat model

libreAI is a single-user, self-hosted application. It intentionally has **no
user accounts and no application-level authentication**. The network boundary
is the security boundary.

## Reporting a vulnerability

Please report security issues privately through the repository's **Security >
Report a vulnerability** (GitHub private security advisory) feature. Do not
open a public issue containing exploit details or real credentials. Include the
affected version, deployment method, and a minimal reproduction. This is a
volunteer project, so no response or fix deadline is guaranteed.

## Trust boundary

Anyone who can reach the HTTP port can:

- read, create, rename, move, export, and delete every project and chat;
- send prompts through every configured provider;
- consume the provider API quota and money associated with your API keys;
- change default models and visibility settings;
- edit the configured `.env` file through the Settings editor, including adding
  or replacing provider endpoints and API keys.

Therefore:

- Keep the default bind address, `127.0.0.1:8080`, and reach it through
  **Tailscale Serve** or an equivalent private, authenticated tunnel.
- Restrict access with tailnet ACLs. Tailscale identity is the access-control
  layer; libreAI does not add another one.
- Do **not** publish the container port on a public interface. The supplied
  Compose file publishes `127.0.0.1:8080:8080` only.
- Do not expose libreAI directly to the public Internet. If you must, put it
  behind a reverse proxy that performs authentication and TLS, and restrict
  which users can reach it. libreAI does not ship an authentication system
  that could make public exposure safe.

Chats and provider credentials are visible to the machine's administrator and
to anyone who can read the database or `.env` file. They are not end-to-end
encrypted.

## Built-in protections

- **Server-side secrets.** Provider API keys are read only from the server
  environment/.env file. Existing keys and custom header values are masked in
  the Settings editor and are never returned by an API. A user who can reach the
  port can replace a key (but cannot read the current one) through the editor.
  Provider diagnostics omit keys and custom header values, and known keys are
  redacted from logged errors.
- **Safe rendering.** Assistant and user Markdown is rendered by an allowlist
  renderer in the browser. Raw HTML is escaped, links are restricted to safe
  schemes, and there is no `innerHTML` use with unsanitized provider output.
- **Browser hardening.** Responses set a strict Content-Security-Policy
  (`default-src 'self'`, no inline script), `X-Content-Type-Options:
  nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`,
  `Cross-Origin-Resource-Policy: same-origin`, and restrictive permissions
  policy headers.
- **Cross-origin requests.** State-changing requests are rejected when the
  browser marks them cross-site or when `Origin` does not match the request
  host. The API does not send CORS permission headers.
- **Input limits.** JSON bodies, message sizes, conversation context, model
  counts, headers, and IDs are bounded and validated. SQL uses bound
  parameters. Provider URLs are fixed by server configuration; the browser
  cannot ask the server to fetch an arbitrary URL.
- **Upstream limits.** Provider requests have configurable timeouts and
  response-size limits. Generation concurrency is bounded
  (`MAX_CONCURRENT_GENERATIONS`), and only one generation per chat is allowed.
- **No shell execution.** libreAI does not invoke a shell, Node.js, or
  user-controlled commands at runtime.
- **Storage permissions.** The database directory is created with mode `0700`,
  the database file with mode `0600`, and the supplied service files run as an
  unprivileged account with a restrictive umask.
- **Safe errors.** Browser-facing errors are generic for internal failures.
  Detailed diagnostics are logged without request bodies, query strings, or
  API keys.

## Operational requirements

1. **Protect the environment file.** Keep it outside Git and use mode `0600`
   for a user-run process, or root-owned mode `0640` with group `libreai` for
   the systemd unit. The application warns at startup if the file is readable
   or writable by users outside its intended owner/group. The Settings editor
   writes this file, so a read-only deployment must make it or its directory
   non-writable for the service account.
2. **Protect the database.** Use full-disk encryption where available. A
   backup contains all chats and must be protected like the live database.
3. **Protect the host.** Run the service as the supplied unprivileged user.
   Do not run it as root. Do not grant extra capabilities or broad filesystem
   access.
4. **Keep dependencies current.** Update the Go toolchain and modules, then
   rebuild and rerun the test suite. See `README.md` for the update procedure.
5. **Use HTTPS for remote access.** Tailscale Serve provides TLS. A manually
   configured reverse proxy must do the same and must preserve the original
   `Host` header (or set a trusted `X-Forwarded-Host`) so origin checks work.
6. **Do not expose the Docker image's internal `0.0.0.0` listener directly.**
   The supplied Compose file publishes only the host loopback address.

## Data retention and deletion

Chats inactive for `CHAT_RETENTION_DAYS` (default 30) are deleted by a cleanup
job at startup and every six hours. Deleting a chat removes its messages from
the live database through foreign-key cascades.

Retention and deletion are **logical**, not cryptographic, erasure. Deleted
pages may remain in SQLite free pages, WAL files, filesystem snapshots, or
backups until overwritten or expired. Use full-disk encryption and a backup
retention policy if this matters for your threat model.

## Provider trust and privacy

Prompts and conversation context are sent to the provider you configure.
Provider responses are untrusted input. Review your provider's retention and
training policies. libreAI does not run local inference and does not proxy
requests through a third-party service other than the provider endpoint you
configure.

## Known limitations

- No accounts, roles, per-user isolation, or audit log.
- No built-in TLS; use Tailscale Serve or a TLS reverse proxy.
- No per-IP rate limiting. Concurrency is limited globally and per chat, but a
  user who can reach the port can still spend provider quota.
- No CSRF token; protection relies on JSON content types plus Origin and
  `Sec-Fetch-Site` checks. Keep the application same-origin and do not add
  permissive CORS proxies.
- The UI and API are not intended as a public multi-tenant service.
- X301 runtime compatibility is documented but may remain unverified until the
  binary is tested on that hardware. A build succeeding on a modern machine is
  not proof of old-CPU compatibility.
