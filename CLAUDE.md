# nclaw

Telegram bot that wraps AI coding CLIs (Claude Code, OpenAI Codex, GitHub Copilot, Google Gemini). Users message a Telegram bot, which invokes the configured CLI backend in a Docker container and returns the response. Each chat/thread gets its own persistent session.

## Working Principles

### Core Principle: KISS

Keep it simple. Always reach for the smallest solution that solves the problem in front of you.

- No speculative abstraction, no premature generalization, no feature you weren't asked for (YAGNI).
- When code repeats, prefer duplication over an abstraction that adds coupling — wait until a pattern is proven before extracting it.
- Fewer moving parts beats clever. Optimize for the next person reading the code, not for the fewest lines.
- Match the surrounding code's idioms, naming, and structure rather than introducing a new style.

### Code Comments

Code explains itself through naming and structure. Default to **no comments**. Add one only when WHY is genuinely non-obvious (a hidden constraint, a bug workaround, a subtle invariant) — never WHAT. Keep it to **one short line**; no docstring-style blocks.

### Repo Etiquette

- Conventional-commit subjects (`feat:`, `fix:`, `refactor:`).
- Commit or push only when asked.
- Never mention "Claude", "Claude Code", or Anthropic in commit messages, PR titles, or descriptions.

### Linters & Cleanup

- Always run `make lint` and `make test` after any change, and fix every issue they report before considering the work done.
- Fix **every** problem you discover in the repo — lint warnings, failing tests, stale docs — even pre-existing and unrelated. Land it as a focused sibling change in the same session.

### CI

After every `git push`, watch the triggered GitHub Actions run to completion and report the result — never push and walk away (`gh run watch`).

## Architecture

```
Telegram ----------->\
Scheduler ------------>  chatqueue (per chat) -> Invoker.Run() -> CLI Backend (cli.Provider) -> Pipeline.Process() -> Telegram
Webhook  ----------->/
```

Three input channels (handler, scheduler, webhook) put their work into a per-chat FIFO queue (`internal/chatqueue`), so runs in one chat never overlap and keep arrival order. Each run goes through the shared `invoker.Invoker`, which builds the same system prompt for every channel and invokes the configured CLI backend via the `cli.Provider` interface. All post-processing (command block execution, stripping, sendfile, reply delivery) is handled by a shared `Pipeline.Process()` call, ensuring consistent behavior across all channels and backends.

## Project Structure

- `cmd/nclaw/main.go` - Entrypoint: config init, DB setup, bot creation, scheduler start, CLI backend selection
- `internal/config/` - Viper-based config (env prefix `NCLAW_`, `.env` support, optional `config.yaml`)
- `internal/cli/` - Generic CLI interfaces (`Client`, `Provider`, `Result`) that all backends implement
- `internal/cli/procrun/` - Runs CLI binaries in their own process group; canceling the context stops the whole tree
- `internal/cli/streamjson/` - Shared stream-json output parser (used by Claude and Claudish adapters), including tool-call events
- `internal/cli/claude/` - Claude Code CLI adapter (fluent builder, stream-json parsing, OAuth token refresh, session storage under `CLAUDE_CONFIG_DIR`)
- `internal/cli/claudish/` - Multi-model CLI adapter (via OpenRouter, Gemini, OpenAI, Ollama, etc.)
- `internal/cli/codex/` - OpenAI Codex CLI adapter (JSONL event parsing, AGENTS.md system prompt)
- `internal/cli/copilot/` - GitHub Copilot CLI adapter (JSONL output, `.github/copilot-instructions.md` system prompt)
- `internal/cli/gemini/` - Gemini CLI adapter (NDJSON stream-json parsing, `GEMINI.md` system prompt)
- `internal/chatqueue/` - Per-chat FIFO queue: batches consecutive user messages, runs scheduler/webhook jobs in line, supports stop/interrupt/snapshot/close
- `internal/invoker/` - Single entry point for CLI runs: working dir (`.isolated` for isolated tasks), system prompt, time header, session reset, timeout
- `internal/handler/` - Telegram message handling, prompt composition (batches, albums, voice transcripts), bot commands (`/stop`, `/new`, `/status`, `/tasks`, `/login`), inline button presses, shutdown notices
- `internal/buttons/` - Inline keyboards (reminder, answer choices, progress controls, task rows) and the callback data they carry
- `internal/progress/` - Status message edited as tool calls stream in, with Stop / Answer new buttons
- `internal/draft/` - Live Telegram draft of the answer being written, for private chats
- `internal/authwatch/` - Warns the admin chat before the backend's stored sign-in expires
- `internal/transcribe/` - Voice/video-note transcription with ffmpeg + whisper.cpp
- `internal/skills/` - Installs missing bundled skills into the CLI skills dirs at startup and updates the copies it made unless they were edited since (hashes in `{data_dir}/.nclaw-skills.json`)
- `internal/blocks/` - Command block patterns (`nclaw:schedule`, `nclaw:webhook`, `nclaw:sendfile`, `nclaw:buttons`) shared by all packages
- `internal/pipeline/` - Unified post-processing: block execution, stripping, sendfile, reply delivery
- `internal/sendfile/` - Shared sendfile processing: parses `nclaw:sendfile` blocks, validates paths, sends documents
- `internal/model/` - GORM models: `ScheduledTask`, `TaskRunLog`, `WebhookRegistration`
- `internal/db/` - Database operations (SQLite with WAL mode)
- `internal/scheduler/` - Task scheduling via `gocron`, command parsing from CLI replies
- `internal/webhook/` - GoFiber HTTP server, webhook manager, and command parsing from CLI replies
- `data/` - Runtime data directory (gitignored)

## Key Patterns

### CLI Backend Interface (`internal/cli/`)
All CLI backends implement two interfaces: `cli.Client` (per-request builder with `Context()`, `Dir()`, `SkipPermissions()`, `AppendSystemPrompt()`, `Ask()`, `Continue()`) and `cli.Provider` (singleton with `NewClient()`, `PreInvoke()`, `Version()`, `Name()`). The `*cli.Result` struct has `Text` (final message for display) and `FullText` (all messages for command block scanning). Consumers use only these interfaces, making them backend-agnostic. Optional capabilities are discovered by type assertion instead of backend names: `NativeSkillsProvider`, `SessionStore` (session size/archive), `StreamingClient` (`OnMessage`), `ProgressClient` (`OnToolUse`), `EphemeralClient` (no session persistence), `AuthProvider` (sign-in expiry and sign-in error detection), `LoginProvider` (interactive sign-in), `MemoryFileProvider` (instructions file the CLI loads from the working directory and its parents).

### CLI Adapters
- **Claude** (`internal/cli/claude/`): Stream-json output parsing via shared `streamjson` package. Claude-specific methods (`Model`, `FallbackModel`, `Resume`) remain on the concrete `*Claude` type. `PreInvoke()` handles OAuth token refresh. Runs pass `--system-prompt-snapshot off`: by default the CLI records the system prompt on a conversation's first request and resends that record on every resume until compaction, so chat context, task list and rule changes would never reach an existing conversation. Changed `CLAUDE.md` files and the date reach it anyway, as attachments appended to the conversation.
- **Claudish** (`internal/cli/claudish/`): Wraps Claude Code via [claudish](https://github.com/MadAppGang/claudish), proxying API calls to alternative providers (OpenRouter, Gemini, OpenAI, Ollama, LM Studio, etc.). Uses the same stream-json output format as Claude, parsed via the shared `streamjson` package. Passes model config (`--model` flag) and model tier overrides (`CLAUDISH_MODEL_OPUS/SONNET/HAIKU/SUBAGENT`) as environment variables. Provider API keys (e.g. `OPENROUTER_API_KEY`, `GEMINI_API_KEY`) pass through from the OS environment. `PreInvoke()` is a no-op.
- **Codex** (`internal/cli/codex/`): JSONL event parsing (`item.completed` with `type: "agent_message"`). System prompt written to `AGENTS.md` in the working directory.
- **Copilot** (`internal/cli/copilot/`): JSONL output parsing via `--output-format=json` flag (`assistant.message` events and `result` for session ID). Session ID tracked per-chat via `.copilot-session-id` file for reliable `--resume` across concurrent chats. `--allow-all`, `--no-ask-user`, `--autopilot` in skip-permissions mode. Optional `--model=MODEL` via `NCLAW_COPILOT_MODEL`. System prompt written to `.github/copilot-instructions.md`. `PreInvoke()` is a no-op.
- **Gemini** (`internal/cli/gemini/`): NDJSON stream-json output parsing (`--output-format stream-json`) with its own event types (message, tool_use, tool_result, error, result). System prompt written to `GEMINI.md` in the working directory. Uses `--approval-mode yolo` for auto-approve. `PreInvoke()` is a no-op.

### OAuth Token Refresh
Before each Claude CLI invocation, `claude.EnsureValidToken()` (called via `Provider.PreInvoke()`) proactively refreshes the OAuth token if it expires within 5 minutes. Credentials are read from `$CLAUDE_CONFIG_DIR/.credentials.json` (default `~/.claude`) using field-preserving JSON round-tripping. The refresh is skipped when `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_AUTH_TOKEN` or `ANTHROPIC_API_KEY` is set. Refresh failures are logged as warnings and do not block the CLI call. Codex and Copilot providers have no-op `PreInvoke()`.

The refresh token itself ends at `claudeAiOauth.refreshTokenExpiresAt`, which refreshing does not extend. The Claude provider implements `cli.AuthProvider`: `authwatch` reads that date every 6 hours and warns the admin chat 5, 2 and 1 day ahead and once it has passed, `/status` shows it, and the pipeline appends a sign-in hint to a failed run whose output is the CLI's authentication error. `/login` (admin's private chat only, `handler.Logins`) runs `claude auth login --claudeai` with piped stdin: the CLI prints the link, the admin's next message is written to its stdin as the code, and the session expires after 10 minutes.

### Chat Queue, Invoker and Commands
Telegram updates are dispatched synchronously (`bot.WithNotAsyncHandlers`); the whitelist is a bot middleware. `Handler.Default` only enqueues: user messages wait a short settle window (longer for albums) and everything queued while a run is busy is answered in one combined prompt. Scheduler and webhook runs are `chatqueue.Job`s in the same lane. `/stop` cancels the lane's run (`procrun` signals the CLI's process group) and drops the queue, `/new` queues a session reset, `/status` reports the lane state, `/tasks` lists the lane's scheduled tasks with pause/resume/cancel buttons. On SIGTERM the queue is closed, interrupted chats are notified, and running work gets up to 20s. Every CLI run gets the shared system prompt (Telegram formatting, the chat's title or the person's name, the topic name, scheduler timezone, task list, skills for non-native backends) plus a current-time header, and is limited by `NCLAW_CLI_TIMEOUT`. `Handler.Default` records chat titles and topic names as `.nclaw-chat-name` / `.nclaw-topic-name` in the chat and topic directories: Telegram has no call to look a topic up, so names come from `forum_topic_created`/`forum_topic_edited` and from the topic's root message that every non-reply message in a topic quotes. In groups each message is prefixed with `[From: name]`. For providers implementing `MemoryFileProvider` (Claude, Claudish) the prompt also points the agent at `<chat>/CLAUDE.md` for facts shared by all topics of a chat, which the CLI loads for every topic because it is a parent of their working directories. Isolated scheduled tasks run in `<chat>/.isolated` so they never become the chat's latest session.

### Unified Pipeline (`internal/pipeline/`)
All post-CLI processing goes through `Pipeline.Process()`, which:
1. **Execute** — Runs each `BlockExecutor.ExecuteBlocks()` on `Result.FullText` (all assistant messages), plus `sendfile.ExecuteBlocks()`. Skipped when the CLI returned an error.
2. **Strip** — Removes all command block syntax (`nclaw:sendfile`, `nclaw:schedule`, `nclaw:webhook`, `nclaw:buttons`) from `Result.Text`. The labels of a reply's last `nclaw:buttons` block (a JSON array, at most 6) become its answer buttons, unless the caller set `Dest.Buttons`.
3. **Append status** — Adds status/error messages from block execution.
4. **Send reply** — Delivers the final text via Telegram with HTML-then-plain-text fallback, splitting long messages.

The scheduler and webhook manager implement the `BlockExecutor` interface and are passed to the pipeline as executors. The handler, scheduler, and webhook packages each invoke the CLI backend independently, then call `Pipeline.Process()` for all post-processing.

### Scheduled Tasks
`nclaw:schedule` code blocks contain JSON commands (`create`, `pause`, `resume`, `cancel`). Tasks support cron, interval, and one-time schedules, in one of three context modes: `group` (continues the chat session), `isolated` (fresh session in `<chat>/.isolated`), `notify` (no CLI run: the prompt is sent verbatim, outside the chat queue, with Done/snooze buttons, and its command blocks are never executed). Tasks persist in SQLite and reload on startup; a task that fails to load is marked `failed` and reported to its chat, and run failures are reported with the task's prompt. The scheduler implements `BlockExecutor` for the pipeline.

### Webhooks
`nclaw:webhook` code blocks contain JSON commands (`create`, `delete`, `list`). Webhooks register HTTP endpoints at `https://{BASE_DOMAIN}/webhooks/{UUID}`. When an external service calls a webhook URL, the request (method, headers, query params, body) is forwarded to the CLI backend in the originating chat via `Continue()`. The HTTP server returns 200 immediately; CLI processing happens asynchronously in a goroutine. Webhooks persist in SQLite alongside scheduled tasks. The webhook manager implements `BlockExecutor` for the pipeline.

### File Handling
- **Inbound**: Attachments (documents, photos, audio, video, stickers) are downloaded to the chat directory as `<name>_<file_unique_id><ext>` and referenced in prompts. Files are cached by unique ID and size. Voice messages and video notes are transcribed with whisper.cpp when `NCLAW_WHISPER_MODEL` is set and passed as text.
- **Outbound**: `sendfile.ExecuteBlocks()` scans for `nclaw:sendfile` code blocks and sends matched files as Telegram documents. Relative paths resolve against the run's working directory; files must resolve to within the chat directory or the OS temp directory; paths outside these locations are rejected.

### Message Formatting
Replies use Telegram HTML formatting with plain-text fallback (tags stripped, entities decoded). Long messages are split by visible UTF-16 length (max 4096), preferring newline boundaries; open tags are closed at each cut and reopened in the next chunk. A reply needing more than 3 chunks is sent as its first chunk plus `answer.md` (`telegram.Markdown` converts the HTML); if the document fails, the remaining chunks follow. Only the first message of a reply quotes the question (`pipeline.Dest.ReplyTo`), and only the last one carries the buttons (`pipeline.Dest.Buttons`; for a reply sent as `answer.md`, its first chunk). The handler marks queued/running/done/failed messages with reactions (`NCLAW_REACTIONS`), and in private chats `draft.Drafter` streams the answer being written (`cli.PartialClient`, Claude's `--include-partial-messages`) through `sendMessageDraft` (`NCLAW_LIVE_DRAFTS`); the draft's stop button acts like `/stop`.

### Inline Buttons (`internal/buttons/`)
Callback data stays under Telegram's 64 bytes: `c:<i>` answer choice, `d` reminder done, `z:<minutes>` snooze (15, 60 or 1440), `s` stop, `n` answer new, `t:<p|r|c>:<task id>` pause/resume/cancel. `Handler.Button` handles every press (the whitelist middleware covers callback queries), answers it with a toast and edits the message:
- a choice is queued as the user's next message (`[Pressed a button under your message "…"]`, plus `[From: name]` in groups) and its keyboard is replaced by `→ label`;
- snooze creates a one-time `notify` task with the reminder's text (`Scheduler.Snooze`), and Done just closes the keyboard;
- ⏹ Stop works like `/stop`; ⏭ Answer new appears while user messages wait and calls `chatqueue.Interrupt`, which cancels the running user batch with `ErrInterrupted` (no reply) and keeps the queue;
- task buttons go through the scheduler's ownership check (`TaskAction`) and redraw the `/tasks` list in place.

Unknown or stale data answers "This button no longer works." The agent learns about `nclaw:buttons` from `telegram.Prompt`.

## Configuration

Required env vars (prefix `NCLAW_`):
- `NCLAW_TELEGRAM_BOT_TOKEN` - Telegram bot token
- `NCLAW_DATA_DIR` - Base directory for session data and files

Optional:
- `NCLAW_CLI` - CLI backend to use: `claude` (default), `claudish` (multi-model), `codex`, `copilot`, or `gemini`. Auto-selects `claudish` when `NCLAW_MODEL` is set
- `NCLAW_CLAUDE_EXEC_PATH` - Full path to the Claude CLI binary (default: `claude` from `PATH`)
- `NCLAW_MODEL` - Model for multi-model backend (e.g. `g@gemini-2.5-pro`, `oai@gpt-4o`)
- `NCLAW_MODEL_OPUS` - Claudish Opus-tier model override
- `NCLAW_MODEL_SONNET` - Claudish Sonnet-tier model override
- `NCLAW_MODEL_HAIKU` - Claudish Haiku-tier model override
- `NCLAW_MODEL_SUBAGENT` - Claudish subagent model override
- `NCLAW_COPILOT_MODEL` - Model for Copilot backend (e.g. `gpt-4.1`)
- `NCLAW_TELEGRAM_WHITELIST_CHAT_IDS` - Comma-separated list of allowed Telegram chat IDs (if unset, bot accepts all chats with a security warning)
- `NCLAW_ADMIN_CHAT_ID` - Chat for the bot's own alerts, e.g. the Claude sign-in about to expire (default: first positive ID in the whitelist; none disables the alerts)
- `NCLAW_LOGIN_EMAIL` - Account email pre-filled on the sign-in page opened by `/login`
- `NCLAW_STARTUP_NOTIFICATION` - Send a "bot started" notification to whitelisted chats on startup (default: `false`)
- `NCLAW_STREAM_MESSAGES` - Send every assistant message from the CLI's JSON stream as a separate reply instead of only the final one (default: `false`). For Claude/Claudish in the Telegram handler, messages are delivered live as they arrive (real-time streaming); other backends/channels split the final buffered output
- `NCLAW_DB_PATH` - SQLite path (default: `{data_dir}/nclaw.db`)
- `NCLAW_MAX_SESSION_BYTES` - Claude Code session transcript size (bytes) past which the session is archived and restarted fresh on the next message (default: `0`, disabled). Only applies to `claude`/`claudish` backends
- `NCLAW_TIMEZONE` - Timezone for scheduler (default: system local)
- `NCLAW_CLI_TIMEOUT` - Maximum duration of one CLI run (default: `60m`; bare numbers are seconds; `0` disables)
- `NCLAW_PROGRESS` - Show a status message with the agent's current step while a request runs (default: `true`)
- `NCLAW_LIVE_DRAFTS` - In private chats, stream the answer being written into a Telegram draft (default: `true`; Claude backend)
- `NCLAW_REACTIONS` - Mark messages with 👀/✍/👌/💔 reactions as they are queued, run, answered or fail (default: `true`)
- `NCLAW_WHISPER_MODEL` - whisper.cpp ggml model path; empty disables voice transcription (set in the Docker images)
- `NCLAW_WHISPER_LANGUAGE` - Spoken-language hint for transcription (default: `auto`)
- `NCLAW_WHISPER_BIN` - whisper.cpp CLI (default: `whisper-cli`)
- `NCLAW_BUNDLED_SKILLS_DIR` - Skills shipped with the image, installed when missing and updated while unedited (default: `/opt/nclaw-skills`)
- `NCLAW_WEBHOOK_BASE_DOMAIN` - Base domain for webhook URLs (required when webhooks enabled)
- `NCLAW_WEBHOOK_PORT` - Webhook HTTP server listen address (default: `:3000`)

## Commands

```
make run             # go run ./cmd/nclaw
make lint            # golangci-lint run ./...
make test            # CGO_ENABLED=1 go test ./...
make docker          # Build and run all-in-one image
make docker-claude   # Build Claude-only image
make docker-multi-model # Build multi-model image
make docker-codex    # Build Codex-only image
make docker-copilot  # Build Copilot-only image
make docker-gemini   # Build Gemini-only image
```

## Code Style

- golangci-lint v2 with gofmt formatter
- Max cyclomatic complexity: 8
- Max line length: 140
- Keep methods small to stay under complexity limit
- Standard Go conventions
- Never mention "Claude Code" in commit messages or PR titles/descriptions
- Always run `golangci-lint run ./...` after any code changes before committing

## Testing

- Always write tests for new code and bug fixes, covering both success and error paths.
- Test behavior, not implementation details.
- Prefer real dependencies over mocks (e.g. in-memory SQLite, `httptest` servers) so tests exercise the actual integration.
- Use `github.com/stretchr/testify` (assert/require)
- Use in-memory SQLite for database tests
- Use `t.TempDir()` for file system tests
- Use `httptest.NewServer` for HTTP-dependent tests
- Test files live next to source files (`*_test.go`)

## Tech Stack

- Go 1.25
- `github.com/go-telegram/bot` - Telegram bot framework
- `github.com/spf13/viper` + `github.com/joho/godotenv` - Configuration
- `gorm.io/gorm` + `gorm.io/driver/sqlite` - Database
- `github.com/go-co-op/gocron/v2` - Task scheduling
- `github.com/gofiber/fiber/v2` - Webhook HTTP server
- `github.com/google/uuid` - Webhook ID generation
- `github.com/stretchr/testify` - Testing

## CI/CD

GitHub Actions pipeline (`.github/workflows/ci.yml`):
1. **Lint** - golangci-lint
2. **Test** - `go test -v ./...`
3. **Release** - GoReleaser cross-compilation (on tag push)
4. **Chocolatey** - Build and push `.nupkg` to Chocolatey (on tag push, Windows runner)
5. **Docker** - Build and push 6 image variants to GHCR on push to main or tagged releases (matrix strategy)
6. **Helm** - Push Helm chart to GHCR OCI registry (on tag push)
7. **Publish** - Promote draft release to published (after all jobs pass)

### Chocolatey Package
The nuspec is generated inline in the CI workflow. It must include: `title` (distinct from id), `summary`, `tags` (space-separated), `packageSourceUrl`, `iconUrl`, and a valid `releaseNotes` URL. Icon lives at `assets/icon.svg` in the repo (served via jsDelivr CDN).

## Docker

A single `docker/Dockerfile` uses multi-stage targets to produce 6 image variants. A shared `base` stage contains all common tools (git, gh CLI, Chromium, Go, Python/uv, ffmpeg, whisper.cpp built in `whisper-builder` with the model from `whisper-model`, bundled skills in `/opt/nclaw-skills`), sets `CLAUDE_CONFIG_DIR=/root/.claude` and runs nclaw under `tini`; each variant adds only its CLI backend:

- `--target all` — All-in-one: Claude Code + Claudish + Codex + Gemini (tagged `latest`)
- `--target claude` — Claude Code only (tagged `claude`)
- `--target multi-model` — Claude Code + Multi-Model (tagged `multi-model`)
- `--target codex` — OpenAI Codex only (tagged `codex`)
- `--target copilot` — GitHub Copilot only (tagged `copilot`)
- `--target gemini` — Gemini CLI only (tagged `gemini`)

The `copilot` variant is Debian-based (`node:24-slim`) because the Copilot CLI needs glibc; it has no Chromium or whisper. CI builds and pushes all six variants to GHCR using a matrix strategy. Custom nclaw skills (`schedule`, `send-file`, `webhook`) are included in all variants; the third-party skills in the Alpine variants.
