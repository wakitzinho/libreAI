# libreAI — Lightweight, Self-Hosted AI Chat

## 1. Your role and objective

You are a senior Go developer, systems engineer, and security-conscious application architect.

Build **libreAI**, a lightweight, self-hosted AI chat application inspired by ChatGPT's interface. It must run reliably on an old ThinkPad X301 with an Intel Core 2 Duo SU9400 processor at 1.4 GHz.

This is a real implementation task, not a design exercise. Inspect the repository, implement the application, run tests, fix errors, and provide working deployment instructions. Do not stop after creating a scaffold or describing what should be implemented.

If the repository already contains files, inspect them before modifying anything. Preserve useful existing work unless it conflicts with the requirements below.

### Core objectives

- Lightweight, fast, and easy to deploy.
- No user accounts, registration, or multi-user infrastructure.
- Go backend.
- Plain HTML, CSS, and vanilla JavaScript frontend.
- SQLite persistence.
- Server-side API keys stored in `.env`.
- OpenAI-compatible API support with configurable providers.
- Server-Sent Events (SSE) for streaming responses.
- Persistent chats organized into projects/folders.
- Automatic chat deletion after 30 days of inactivity.
- Catppuccin-inspired interface with dark and light themes.
- Native Linux deployment and optional Docker deployment.
- Strict compatibility with the ThinkPad X301's old CPU.

Do not introduce unnecessary dependencies or features simply because they are common in modern AI applications.

---

## 2. Mandatory hardware compatibility requirements

**The target CPU is an Intel Core 2 Duo SU9400 (Penryn), not a modern x86-64 processor. CPU compatibility is a hard requirement.**

The application must not depend on AVX, AVX2, AVX-512, or any other instruction set that the target CPU does not support.

The processor supports an older x86-64 instruction baseline and SSE4.1, but not AVX. Verify the actual capabilities when testing on hardware.

### Build requirements

1. Target `linux/amd64` for the initial Linux deployment.
2. Build Go binaries with `GOAMD64=v1`.
3. Do not use `-march=native`, `-mtune=native`, or compiler flags that silently optimize for the development machine.
4. Do not assume that a binary built on a modern x86-64 computer is compatible merely because it targets `amd64`.
5. Inspect the complete dependency chain for CPU-specific assumptions, native code, CGo requirements, and bundled binaries.
6. Avoid dependencies that require AVX, AVX2, or other unsupported instructions.
7. Prefer pure Go implementations where they reduce compatibility risks and simplify deployment.
8. Avoid requiring Node.js, npm, Bun, Deno, a JavaScript runtime, or a frontend compilation toolchain on the server.
9. Do not require Chromium, Electron, WebKit, a desktop environment, or a browser on the server.
10. Do not run local LLM inference. AI computation is performed by remote providers.

### Dependency selection

Use the Go standard library whenever practical.

Select dependencies deliberately. Before adopting a third-party package, verify that it:

- Supports the selected Go version.
- Does not require unsupported CPU instructions.
- Does not introduce unnecessary native dependencies.
- Has an acceptable license and maintenance status.
- Does not require a separate background service without a compelling reason.

For SQLite, evaluate a pure-Go driver such as `modernc.org/sqlite`. Verify its current compatibility, Go-version requirements, platform support, and dependency tree before selecting it.

A CGo-based SQLite driver is acceptable only if there is a demonstrated reason to use it and the deployment process reliably builds it for the X301's CPU baseline.

Do not assume that pure Go automatically guarantees CPU compatibility.

### Required verification

Provide:

- The exact Go version used for the build.
- The effective CPU architecture target and build settings.
- A list of native dependencies, if any.
- Commands for inspecting the resulting binary and its linked libraries.
- A documented test procedure for the actual ThinkPad X301.
- A clear distinction between verified results and compatibility assumptions.

The preferred verification is to build and run the application on the X301 itself.

If the development environment cannot access the X301, use a documented conservative build process and perform static and dependency checks. Explicitly state that final runtime compatibility remains unverified until tested on the target hardware.

Never claim that the application has been tested on the X301 unless it actually has.

---

## 3. Technology stack

### Backend

- Go.
- Go standard library HTTP server and routing where practical.
- `database/sql` with a verified SQLite driver.
- SSE implemented using HTTP streaming and flushing.
- Embedded static frontend assets using Go's `embed` package.
- `context.Context` for request cancellation and timeouts.
- Structured logging using the standard library or a small, justified dependency.

Avoid a large web framework unless there is a concrete technical justification.

### Frontend

- HTML.
- CSS.
- Vanilla JavaScript.
- Browser Fetch API and `ReadableStream` for SSE consumption.
- No React, Next.js, Vue, Angular, Svelte, or other frontend frameworks.
- No required frontend build step.
- No runtime CDN dependencies.
- No externally hosted JavaScript, CSS, fonts, or icons required for normal operation.

All frontend assets must be served directly by the Go application.

Use CSS variables for colors and themes. Prefer system fonts and small inline SVG icons where necessary.

### Database

SQLite is the only required database.

Do not introduce PostgreSQL, MySQL, Redis, MongoDB, or another database service.

Use migrations so the schema can evolve safely. Enable foreign-key enforcement and choose an appropriate SQLite journal and busy-timeout configuration for a single-instance application.

Use transactions for operations that modify multiple related records.

---

## 4. Application architecture

Implement a modular monolithic application. Do not split it into microservices.

Suggested layout:

    libreai/
    ├── cmd/
    │   └── libreai/
    │       └── main.go
    ├── internal/
    │   ├── config/
    │   ├── database/
    │   ├── chats/
    │   ├── projects/
    │   ├── providers/
    │   ├── streaming/
    │   ├── retention/
    │   └── httpserver/
    ├── web/
    │   ├── index.html
    │   ├── css/
    │   │   └── app.css
    │   ├── js/
    │   │   ├── app.js
    │   │   ├── api.js
    │   │   ├── chat.js
    │   │   └── sidebar.js
    │   └── assets/
    ├── migrations/
    ├── tests/
    ├── .env.example
    ├── .gitignore
    ├── Dockerfile
    ├── compose.yaml
    ├── go.mod
    ├── go.sum
    ├── Makefile
    ├── README.md
    ├── SECURITY.md
    └── LICENSE

This is a suggested organization, not a requirement to create empty packages or excessive abstractions. Keep the final code simple and cohesive.

The Go server must embed and serve the frontend. The browser must communicate with the Go backend over the same origin.

Do not create separate frontend and backend servers in production.

---

## 5. API provider architecture

Support the OpenAI-compatible Chat Completions API format as the initial provider interface.

**OpenAI-compatible does not mean OpenAI-only.** Other providers can be used when they implement the necessary compatible request, response, and streaming formats.

The implementation must allow a provider to define:

- Display name.
- Base URL.
- Server-side API key.
- Model IDs.
- Whether model discovery is supported.
- Optional custom headers only where necessary and configured securely.
- Provider-specific timeouts or compatibility options when justified.

A provider must not be hardcoded to the official OpenAI endpoint.

### Configuration

Use a server-side `.env` file for provider configuration.

Support multiple configured providers and their API keys. Include a clearly documented `.env.example` with placeholders, not real credentials.

For example, the configuration may conceptually contain:

    HOST=127.0.0.1
    PORT=8080
    DATABASE_PATH=./data/libreai.db
    ENV_FILE=.env

    PROVIDER_OPENAI_ENABLED=true
    PROVIDER_OPENAI_BASE_URL=https://api.openai.com/v1
    PROVIDER_OPENAI_API_KEY=
    PROVIDER_OPENAI_MODELS=

    PROVIDER_GROQ_ENABLED=false
    PROVIDER_GROQ_BASE_URL=
    PROVIDER_GROQ_API_KEY=
    PROVIDER_GROQ_MODELS=

    CHAT_RETENTION_DAYS=30

This is illustrative. Design a consistent naming scheme that supports multiple providers without an unnecessarily complex configuration format.

Do not require every listed provider to be configured. An absent API key must disable that provider gracefully.

If an environment variable is explicitly set, define clearly whether it overrides the corresponding `.env` value.

### Security requirements

- `.env` must be excluded from Git.
- Recommend permissions of `0600` for `.env`.
- Never return API keys in API responses.
- Never embed provider credentials in HTML or JavaScript.
- Never log API keys, authorization headers, or complete sensitive environment values.
- Do not expose arbitrary server environment variables through the settings interface.
- Do not permit the browser to supply an arbitrary provider URL on each request.

Provider configuration is trusted server-side configuration, not user-controlled input.

### Model discovery

Implement model discovery where supported.

For compatible providers, attempt to use their model-list endpoint when available. Treat model discovery as an optional provider capability rather than a universal guarantee.

Requirements:

- Display discovered models in the model selector.
- Permit explicitly configured model IDs when discovery is unsupported or fails.
- Preserve manually configured models when an API request fails.
- Do not assume that every returned model supports chat completions, SSE, vision, or every optional parameter.
- Display provider and model names clearly.
- Handle unavailable models and invalid model IDs gracefully.
- Cache discovery results in memory where useful, without adding another database or caching service.
- Allow the user to refresh model discovery.

The settings interface may allow model visibility and default-model selection. API keys and provider endpoints remain in server-side configuration.

Changing `.env` should be documented as requiring a restart unless a safe reload mechanism is deliberately implemented.

### Provider-specific compatibility

Do not claim that all OpenAI-compatible services support exactly the same features.

Start with ordinary text chat and streaming. Send only parameters supported by the configured provider.

Do not automatically retry a request against another model or provider after partial output has already been streamed to the user. Avoid silently duplicating requests or incurring unexpected API costs.

---

## 6. Chat interface and streaming

The interface should resemble ChatGPT's familiar layout without copying proprietary branding.

### Main layout

A responsive interface consisting of:

- Collapsible left sidebar.
- New chat button.
- Searchable or navigable chat history where feasible.
- Project/folder list.
- Chat titles.
- Model selector.
- Main conversation area.
- Message composer fixed near the bottom of the conversation.
- Settings entry at the bottom of the sidebar.
- Clear loading, error, and empty states.

On mobile, the sidebar should become a usable drawer.

### Message behavior

Support:

- User messages and assistant responses.
- Markdown rendering.
- Syntax-highlighted code blocks.
- Copy message and copy code buttons.
- Regenerate the most recent assistant response.
- Edit and resend a previous user message.
- Stop generation.
- Automatic conversation titles, using a simple deterministic method such as the first user message initially.
- Conversation export as Markdown and JSON.

Keep the initial implementation focused. Do not implement voice, tools, RAG, file uploads, image generation, or a plugin system unless explicitly requested later.

### SSE streaming

Use Server-Sent Events for streamed responses.

The backend must:

1. Validate the incoming request.
2. Load the conversation and selected provider/model.
3. Send the request to the provider with a streaming option.
4. Parse upstream SSE incrementally.
5. Forward normalized text deltas to the browser.
6. Save the completed assistant message to SQLite.
7. Report errors through a well-defined stream event or error response.
8. Release resources when generation finishes or is cancelled.

The frontend must append text incrementally rather than waiting for the complete response.

Implement cancellation using request contexts and HTTP request cancellation. Provide a Stop button that aborts the generation and propagates cancellation upstream when supported.

Handle:

- Provider HTTP errors.
- Rate limits.
- Timeouts.
- Network disconnects.
- Malformed SSE events.
- Missing or unexpected response fields.
- Stream termination without a normal completion event.
- Browser navigation away from an active chat.

Do not treat a failed or interrupted generation as a successful completed response.

Persist the partial assistant response according to a clearly documented policy. Never leave the database in an inconsistent state.

Use appropriate HTTP headers for SSE, flush each event promptly, and disable response buffering where applicable.

### Markdown security

Markdown rendering must not introduce cross-site scripting vulnerabilities.

Use a small, CPU-compatible rendering approach or implement a limited renderer if that is genuinely simpler and safer.

Do not render arbitrary assistant or user HTML as trusted content.

Escape HTML by default, sanitize any permitted HTML, and safely handle links. Consider `rel="noopener noreferrer"` for external links opened in a new tab.

Do not introduce a large browser framework just to render Markdown.

---

## 7. Projects, folders, and chat persistence

Implement projects as folders that contain chats.

### Required operations

Projects:

- Create.
- Rename.
- Delete.
- Display in the sidebar.

Chats:

- Create.
- Rename.
- Open.
- Delete.
- Move between projects.
- Remain outside a project when not assigned to one.
- Export as Markdown or JSON.

Deleting a project should not unexpectedly destroy its chats. Prefer moving its chats into the unfiled/default area, and document the behavior.

Use SQLite foreign keys and transactions to maintain referential integrity.

### Suggested data model

Projects:

- ID.
- Name.
- Created timestamp.
- Updated timestamp.

Chats:

- ID.
- Title.
- Project ID, nullable.
- Provider ID.
- Model ID.
- Created timestamp.
- Updated timestamp.
- Last activity timestamp.

Messages:

- ID.
- Chat ID.
- Role.
- Content.
- Created timestamp.
- Status where necessary for incomplete or interrupted generations.

Use appropriate indexes for chat lookup, message retrieval, project membership, and expiration queries.

Use UTC timestamps internally and handle conversion for display.

Avoid storing duplicate copies of data that can be derived safely.

### Chat persistence

Persist messages as they are submitted and generated. A browser refresh must not destroy saved chats.

Load only the data required for the current view. Avoid reloading the entire conversation database after every action.

Use prepared statements and parameterized SQL queries.

---

## 8. Automatic 30-day retention

Automatically delete chats after 30 days of inactivity.

A chat's expiration timestamp is based on its `last_activity_at` value:

    expires_at = last_activity_at + 30 days

Opening a chat or sending a message resets its activity timestamp. Merely refreshing unrelated pages must not reset the activity of every conversation.

Define activity consistently in both the frontend and backend. A successful chat open and a submitted message count as activity. An automatic background refresh does not.

### Implementation requirements

- Run a lightweight cleanup routine at startup and periodically thereafter.
- Use a configurable retention period, defaulting to 30 days.
- Delete expired chats and their messages in a transaction.
- Handle projects containing chats that expire without deleting the projects themselves.
- Use database constraints and cleanup logic to prevent orphaned messages.
- Do not require an external scheduler or background service.
- Avoid excessively frequent full-table scans.
- Ensure retention works even if the server has been offline for several days.
- Log the number of deleted chats, but not their message contents.

Document that deletion removes the corresponding SQLite records but is not necessarily forensic erasure from SSD storage, filesystem snapshots, or backups.

---

## 9. SQLite storage and recovery

The application should create its data directory and initialize its database automatically.

Requirements:

- Configurable database path.
- Automatic schema migrations.
- Foreign-key enforcement.
- Sensible busy timeout.
- Appropriate transaction handling.
- WAL mode if suitable for the chosen deployment.
- Graceful database shutdown.
- Clear startup errors when the database cannot be opened.
- Backup and restore instructions.

Do not store the SQLite database in a container's ephemeral filesystem.

Do not delete or overwrite an existing database during startup or migration.

Document how to stop the service, make a consistent backup, and restore it.

A single application instance is the intended deployment. Do not add distributed locking or multi-instance infrastructure.

---

## 10. Catppuccin interface

Use a Catppuccin-inspired palette, with Latte as the light theme and Mocha as the dark theme.

Use CSS custom properties to define the theme.

Requirements:

- Dark theme by default.
- Light/dark toggle in settings.
- Persist the theme preference in browser local storage.
- Respect system preferences on first visit if practical.
- Consistent spacing, typography, borders, and corner radii.
- Clear visual distinction between user and assistant messages.
- Accessible focus states and keyboard navigation.
- Responsive layout for desktop and mobile.
- No mandatory external fonts, icon libraries, or CDNs.
- Minimal animation, with a preference for reduced-motion settings.

The application should feel polished but remain simple. Avoid unnecessary gradients, elaborate animations, and dashboard-style clutter.

### Sidebar behavior

The sidebar should support:

- New chat.
- Recent conversations.
- Project folders that can expand and collapse.
- Moving chats into projects.
- Renaming and deleting chats.
- Project creation and renaming.
- Settings at the bottom.
- Collapsing and reopening the sidebar.

Use a straightforward interaction model. Avoid complex drag-and-drop if a simpler move-to-project menu is more reliable.

---

## 11. Settings

Provide a lightweight settings page or panel.

Include:

- Current provider/model selection.
- Default provider/model selection.
- Model discovery refresh.
- Model visibility controls if straightforward.
- Theme selection.
- Chat retention information.
- Application version.
- Provider configuration status without displaying credentials.

Do not expose raw API keys in the browser.

Do not allow arbitrary environment editing through the web interface.

Since provider keys live in `.env`, document how to edit the file and restart the application.

Do not add accounts, profiles, teams, user roles, or a multi-user administration interface.

---

## 12. Security and Tailscale deployment

The intended deployment is a personal application accessible through Tailscale, not a publicly exposed AI service.

### Default network behavior

Bind the application to `127.0.0.1:8080` by default.

For a native deployment, document how to use Tailscale Serve as the private HTTPS entry point to the local application.

For Docker, publish the container port to host loopback only, such as `127.0.0.1:8080:8080`, and use the host's Tailscale Serve configuration.

Do not bind to all interfaces by default.

Do not configure router port forwarding or public access automatically.

Tailscale protects network access according to the tailnet's identity and access-control configuration. No application accounts are required, but anyone authorized to reach the application may be able to use its configured API keys indirectly and access its chats. Explain this clearly.

### Application security

Implement:

- Strict input validation.
- Parameterized SQL.
- Safe HTML and Markdown handling.
- Reasonable request body limits.
- Configurable upstream request timeouts.
- Safe error messages that do not expose secrets or filesystem paths.
- Appropriate security headers.
- Restrictive handling of cross-origin requests.
- Safe filesystem permissions.
- Graceful handling of malformed requests.
- No arbitrary URL fetching by the backend.
- No shell execution based on user input.

Do not add a complex authentication system merely to claim the application is secure. Instead, document the trust boundary and protect the network entry point.

If the app is accessed outside the private tailnet, require a separate, explicitly configured access-control mechanism rather than assuming that binding to localhost alone provides authentication.

---

## 13. Native systemd deployment

Provide a production-ready native deployment for Linux, with NixOS as the primary target.

The native application should be a compiled Go binary, not a collection of scripts requiring an interpreter at runtime.

### Requirements

- A systemd service unit.
- Automatic restart after failure.
- Start after required local filesystem and network dependencies.
- Dedicated unprivileged service account where appropriate.
- Explicit writable data directory.
- Explicit `.env` location.
- Restrictive filesystem permissions.
- No unnecessary Linux capabilities.
- No access to unrelated home directories.
- Persistent SQLite storage.
- Clear journal logging.
- No desktop environment or graphical dependencies.

Use systemd hardening options where compatible with the application's needs. Test the actual write paths before enabling restrictive filesystem settings.

The service should not need root privileges to serve the chat interface or communicate with remote AI providers.

### NixOS

Provide either a suitable NixOS module or complete configuration examples and instructions.

Explain how to:

- Build or package libreAI.
- Configure its data directory.
- Store `.env` outside Git.
- Create the service account.
- Enable the systemd unit.
- Start and inspect the service.
- Update the binary without deleting user data.
- Configure Tailscale Serve.
- Back up the database.

Do not hardcode personal machine-specific paths or secrets.

Do not require a permanent desktop environment.

---

## 14. Optional Docker deployment

Docker is an alternative deployment method, not a requirement for the native installation.

Provide:

- A Dockerfile.
- A Compose configuration.
- Persistent database storage.
- A read-only application filesystem where practical.
- A non-root runtime user.
- A health check that does not require a public endpoint.
- Correct signal handling and graceful shutdown.
- Environment-file support.
- A documented upgrade and backup procedure.

### CPU compatibility in Docker

Every executable in the image must be compatible with the X301.

Do not assume that an `amd64` image automatically supports the Core 2 Duo.

Build the Go binary with the correct CPU baseline. Avoid runtime dependencies on Node.js or other unnecessary runtimes.

Prefer a minimal runtime image. A static Go binary with the required CA certificates and any required timezone data is a reasonable approach if the selected SQLite driver supports it.

If using a multi-stage Docker build, ensure the build stage produces a compatible binary and the runtime image includes everything it needs.

Do not use `scratch` blindly if the application requires certificates, timezone data, shared libraries, or other runtime files.

The Compose configuration must:

- Bind the published port to host loopback.
- Persist the SQLite database.
- Provide `.env` without baking secrets into the image.
- Avoid privileged mode.
- Avoid host networking unless specifically justified.
- Avoid mounting the Docker socket.
- Avoid mounting the entire host filesystem.

Document that the native systemd deployment is expected to have the smallest and simplest runtime footprint.

---

## 15. Resource usage and performance

The target is an old laptop that may run other self-hosted services simultaneously.

Prioritize predictable, low overhead.

- No local AI inference.
- No background indexing service.
- No separate Redis instance.
- No frontend build server.
- No unnecessary polling.
- No unbounded in-memory conversation history.
- No repeated full database scans.
- No excessive database connections.
- No unnecessary goroutines or timers.
- No large framework dependencies without justification.
- No verbose per-token logging.
- No unbounded request bodies or upstream responses.

Use request cancellation and bounded concurrency. Set sensible limits for concurrent generations so the old CPU and memory are not overwhelmed by accidental parallel requests.

Do not introduce a complicated caching layer prematurely.

Avoid premature optimization: implement a correct application first, then measure actual resource use.

---

## 16. Error handling and reliability

The application must handle failures without corrupting existing chats.

Provide useful messages for:

- Missing API key.
- Invalid provider configuration.
- Unavailable model.
- Provider rate limits.
- Invalid API responses.
- Upstream timeout.
- Network failure.
- Cancelled generation.
- SQLite errors.
- Invalid JSON.
- Expired or missing chat IDs.
- Failed migrations.
- Insufficient filesystem permissions.

Do not show raw stack traces or secrets to users.

Log diagnostic details on the server, with sensitive values redacted.

Use HTTP status codes consistently.

The service should recover from temporary provider failures without needing a restart.

Do not silently discard saved messages when an API request fails.

---

## 17. Testing requirements

Create automated tests and actually run them.

### Backend tests

Cover:

- Configuration loading and validation.
- Missing and malformed provider configuration.
- Database initialization and migrations.
- Project CRUD.
- Chat CRUD.
- Moving chats between projects.
- Message persistence.
- Foreign-key behavior.
- 30-day expiration.
- Activity timestamp updates.
- Retention cleanup.
- Provider request formatting.
- SSE parsing and forwarding.
- Provider HTTP errors.
- Cancellation.
- Concurrent database access where relevant.
- Exported Markdown and JSON.
- API-key redaction.

Use mock HTTP servers to test provider behavior without real API keys.

Do not require paid API calls to run the normal test suite.

### Frontend checks

Verify:

- Sidebar navigation.
- Project operations.
- Chat creation and loading.
- Model selection.
- Streaming output.
- Stop generation.
- Markdown and code rendering.
- Copy actions.
- Regeneration and editing.
- Export.
- Theme persistence.
- Mobile layout.
- Error states.

Do not add a large browser-testing framework without justification. If a full browser test cannot be run in the development environment, state what was tested and what remains unverified.

### CPU compatibility checks

At minimum:

1. Run Go tests.
2. Build with `GOAMD64=v1`.
3. Inspect native dependencies and linked libraries.
4. Inspect any bundled executable or third-party binary.
5. Confirm the application starts and serves static assets.
6. Run an end-to-end mock-provider streaming test.
7. Test the resulting deployment on the actual X301 when available.

A modern-machine build is not proof of actual hardware compatibility.

---

## 18. Documentation

Write a complete README that explains:

- What libreAI is.
- Supported architecture and hardware limitations.
- Prerequisites.
- Quick start.
- `.env` configuration.
- Adding providers.
- Model discovery.
- Running tests.
- Building for the X301.
- Native installation.
- NixOS/systemd deployment.
- Docker deployment.
- Tailscale access.
- Database location.
- Backup and restore.
- Automatic chat deletion.
- Troubleshooting.
- Updating and rollback.

Include exact commands that match the files you create.

Do not document options that the implementation does not actually support.

Include a `.env.example`, `.gitignore`, `SECURITY.md`, and an appropriate open-source license. If the repository has an existing license, preserve it rather than replacing it without justification.

---

## 19. Implementation sequence

Implement in small, testable phases.

### Phase 1: Inspect and plan

- Inspect the repository and target Go toolchain.
- Confirm the intended directory structure.
- Identify CPU-sensitive dependencies.
- Decide on the SQLite driver after compatibility evaluation.
- Write a concise implementation plan.

### Phase 2: Backend foundation

- Configuration loading.
- SQLite initialization and migrations.
- Embedded static-file serving.
- Basic health endpoint.
- Project and chat persistence.
- Retention cleanup.

### Phase 3: Provider integration

- Configurable OpenAI-compatible providers.
- API-key handling.
- Model discovery and manual fallback.
- SSE streaming.
- Cancellation and error handling.
- Mock-provider tests.

### Phase 4: Frontend

- Catppuccin layout and themes.
- Sidebar and projects.
- Chat history.
- Message composer.
- Streaming display.
- Markdown and code blocks.
- Stop, regenerate, and edit/resend.
- Export.

### Phase 5: Security and hardening

- Input validation.
- Markdown sanitization.
- API-key protection.
- Request limits.
- Network defaults.
- Database integrity.
- Error handling.

### Phase 6: Deployment

- Native Go build.
- systemd unit.
- NixOS instructions.
- Dockerfile and Compose.
- Tailscale access.
- Backup and restore.

### Phase 7: Verification

- Run all available tests.
- Fix failures rather than documenting them away.
- Verify the final binary's CPU baseline and dependencies.
- Test a complete streamed conversation against a mock provider.
- Verify chat persistence across restart.
- Verify expiration behavior.
- Produce a final implementation report.

Each phase should leave the repository in a usable state. Avoid generating the entire application in one enormous, untested code dump.

---

## 20. Definition of done

The project is complete only when all of the following are true:

- [ ] Go backend serves the complete frontend.
- [ ] Frontend uses HTML, CSS, and vanilla JavaScript.
- [ ] No Node.js runtime or frontend build step is required on the server.
- [ ] SQLite stores projects, chats, and messages.
- [ ] Chat folders support creation, renaming, deletion, and moving chats.
- [ ] Chat history survives application restarts.
- [ ] Chats expire after 30 days of inactivity.
- [ ] API keys remain exclusively server-side.
- [ ] Multiple OpenAI-compatible providers can be configured.
- [ ] Model discovery works where supported.
- [ ] Manually configured models work when discovery is unavailable.
- [ ] SSE streams responses incrementally.
- [ ] Generation can be cancelled.
- [ ] Markdown and syntax-highlighted code render safely.
- [ ] Copy, regenerate, edit/resend, and export work.
- [ ] Catppuccin dark/light themes work.
- [ ] Tailscale deployment is documented and defaults to private access.
- [ ] Native systemd deployment works.
- [ ] Optional Docker deployment uses persistent storage and loopback-only port publishing.
- [ ] Dependencies and binaries have been checked for old-CPU compatibility.
- [ ] Automated tests pass.
- [ ] Documentation matches the actual implementation.
- [ ] No API keys, personal credentials, or database files are committed.
- [ ] No unimplemented feature is falsely presented as complete.

## Final instructions

Make reasonable engineering decisions without asking for approval for every small detail.

If a requirement conflicts with CPU compatibility, security, or data integrity, prioritize those constraints and document the trade-off.

Do not replace Go with Python or Node.js. Do not replace SQLite with a server database. Do not add user accounts. Do not silently drop the 30-day retention requirement. Do not substitute a generic dashboard for the requested chat interface.

Do not stop at a plan, scaffold, mockup, or collection of placeholder handlers.

**Deliver a working, tested, documented libreAI application that is designed around the actual limitations of the ThinkPad X301, rather than a modern application that merely happens to compile for amd64.**
