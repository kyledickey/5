# Configuration

## Runtime Dependencies

The process expects:

- MySQL, connected through `DATABASE_DSN` in `apps/backend/internal/store/connect_db.go`
- Redis, connected through `REDIS_URL` in `apps/backend/internal/store/connect_redis.go`
- Discord bot credentials and OAuth settings loaded in `apps/backend/internal/config/config.go`

The startup path fails fast on missing critical storage env vars and on failed
DB or Redis pings. See `apps/backend/internal/config/config.go`, `apps/backend/internal/store/connect_db.go`, and
`apps/backend/internal/store/connect_redis.go`.

## Environment Variables

Environment bindings are declared on the structs in `apps/backend/internal/config/types.go`
and decoded by `github.com/caarlos0/env/v11`. Defaults live in `config.Default()`.
Empty numeric and boolean values retain their defaults; malformed non-empty values
fail startup. Validation checks the loaded configuration without rereading the environment.

In development, `.env` fills missing process variables without changing the process
environment. Explicit process values take precedence.

| Variable | Required | Purpose |
| --- | --- | --- |
| `ENVIRONMENT` | no | Runtime mode. Defaults to `dev`. |
| `API_PORT` | no | API listen port. Defaults to `8080`. |
| `OPS_STATUS_TOKEN` | no | Enables global `GET /ops/status` when supplied and matched by `X-Quack-Ops-Key`. |
| `API_CORS_ALLOWED_ORIGINS` | required outside `dev` | Comma-separated exact dashboard origins. Wildcards and malformed origins fail startup. Development defaults to localhost ports `3000`. |
| `API_TRUSTED_PROXIES` | no | Comma-separated proxy IPs/CIDRs allowed to supply forwarded client IPs. Empty disables forwarded-IP trust. |
| `API_MAX_BODY_BYTES` | no | Maximum request body size. Defaults to `1048576`. |
| `API_READ_HEADER_TIMEOUT_SECONDS` | no | Header-read bound. Defaults to `5`. |
| `API_READ_TIMEOUT_SECONDS` | no | Whole-request read bound. Defaults to `15`. |
| `API_WRITE_TIMEOUT_SECONDS` | no | Response-write bound. Defaults to `30`. |
| `API_IDLE_TIMEOUT_SECONDS` | no | Keep-alive idle bound. Defaults to `60`. |
| `DATABASE_DSN` | yes | MySQL DSN for GORM. |
| `REDIS_URL` | yes | Redis connection URL. |
| `DISCORD_TOKEN` | yes | Discord bot token. |
| `DISCORD_APP_ID` | yes | Discord application ID. |
| `DISCORD_CLIENT_SECRET` | needed for OAuth | Discord OAuth client secret. |
| `DISCORD_OAUTH_REDIRECT_URI` | needed for OAuth | OAuth callback URI used by `/auth/discord/callback`. |
| `DISCORD_OAUTH_SCOPES` | no | OAuth scopes. Defaults to `identify guilds`. |
| `DISCORD_COMMAND_GUILD_ID` | no | Optional test-guild command sync target. |
| `DISCORD_COMMAND_PRUNE` | no | Enables command pruning on sync. Defaults to `false`. |
| `AUTH_SESSION_COOKIE_NAME` | no | Session cookie name. Defaults to `quack_session`. |
| `AUTH_CSRF_COOKIE_NAME` | no | Non-HttpOnly double-submit token cookie. Defaults to `quack_csrf`. |
| `AUTH_SESSION_TTL_HOURS` | no | Session TTL in hours. Defaults to `168`. |
| `AUTH_STATE_TTL_MINUTES` | no | OAuth state TTL in minutes. Defaults to `10`. |
| `AUTH_POST_LOGIN_REDIRECT` | no | Default post-login redirect path. Defaults to `/`. |
| `AUTH_COOKIE_SECURE` | no | Secure-cookie toggle. Defaults to `true` outside `dev`. |
| `RATE_LIMIT_<CLASS>_MAXIMUM` | no | Fixed-window capacity for `OAUTH`, `MEMBER_READ`, `TEMPLATE_WRITE`, `CASE_CREATE`, `RETRY`, or `EVIDENCE`. See `docs/http-api-platform.md`. |
| `RATE_LIMIT_<CLASS>_WINDOW_SECONDS` | no | Positive fixed-window duration for the matching class. |
| `HTTP_IDEMPOTENCY_TTL_HOURS` | no | Completed HTTP replay retention. Defaults to `24`. |
| `EVENT_QUEUE_SIZE` | no | In-process queue buffer size. Defaults to `1000`. |
| `EVENT_QUEUE_WORKERS` | no | Number of queue workers. Defaults to `3`. |

`.env.example` mirrors the local Compose workflow and includes the same
development-oriented defaults used by `compose.yaml`. The app service profile in
`compose.yaml` also injects container-local values for `DATABASE_DSN` and
`REDIS_URL`, so those two settings do not need to point at `127.0.0.1` when the
service runs inside Compose.

## Compose-Specific Notes

The dependency-only workflow and the full app-container workflow use the same
env names, but with different responsibilities:

- `docker compose up -d` expects your host-side `.env` to point at
  `127.0.0.1:3306` and `127.0.0.1:6379`
- `docker compose --profile app up --build` injects container-local
  `DATABASE_DSN` and `REDIS_URL` values for the app service
- Discord and auth-related env vars still need to be populated by your local
  `.env` when the app profile is enabled

In practice, the variables that matter specifically for the app container
profile are the app runtime settings and Discord/auth credentials from
`.env.example`, especially:

- `ENVIRONMENT`
- `API_PORT`
- `OPS_STATUS_TOKEN`
- `API_CORS_ALLOWED_ORIGINS`
- `API_TRUSTED_PROXIES`
- `API_MAX_BODY_BYTES`
- `API_READ_HEADER_TIMEOUT_SECONDS`, `API_READ_TIMEOUT_SECONDS`, `API_WRITE_TIMEOUT_SECONDS`, `API_IDLE_TIMEOUT_SECONDS`
- `DISCORD_TOKEN`
- `DISCORD_APP_ID`
- `DISCORD_CLIENT_SECRET`
- `DISCORD_OAUTH_REDIRECT_URI`
- `DISCORD_COMMAND_GUILD_ID`
- `DISCORD_COMMAND_PRUNE`
- `AUTH_SESSION_COOKIE_NAME`
- `AUTH_CSRF_COOKIE_NAME`
- `AUTH_SESSION_TTL_HOURS`
- `AUTH_STATE_TTL_MINUTES`
- `AUTH_POST_LOGIN_REDIRECT`
- `AUTH_COOKIE_SECURE`
- `RATE_LIMIT_*` and `HTTP_IDEMPOTENCY_TTL_HOURS`
- `EVENT_QUEUE_SIZE`
- `EVENT_QUEUE_WORKERS`

## Local Auth and CORS Notes

Development defaults allow credentialed browser requests from
`http://localhost:3000` and `http://127.0.0.1:3000`. Configure exact production
origins with `API_CORS_ALLOWED_ORIGINS`; an empty, wildcard, or malformed
production allowlist fails startup. Cookie-authenticated writes also require the
`X-CSRF-Token` header to match the `quack_csrf` cookie and must originate from
the configured dashboard origin.

## Discord Install Permissions and Intents

The install URL needs the `bot` and `applications.commands` OAuth scopes. Core
case responses require the bot to view the invoking staff channel, send
messages, embed links, and read message history. Configured v5 enforcement also
requires the bot's `Moderate Members`, `Kick Members`, or `Ban Members`
permission for the action an admin places on a template; V5-003 owns the live
actor/bot permission and hierarchy preflight before a punitive case is created.

The managed-evidence slice will additionally require `Manage Channels` and
permission-overwrite access to create and repair its staff-only channel. Merely
persisting the configured evidence-channel reference in the current guild
settings contract does not create or permission that channel. Audit mirroring
uses the normal view/send/embed permissions in its selected staff channel.

Gateway intent needs by product surface are:

- Core guild lifecycle and application-command interactions: `Guilds`; no
  privileged intent is inherently required for the current setup/settings
  flow.
- Message evidence, honeypot messages, and message-based general logging:
  `Guild Messages` plus the privileged `Message Content` intent when content is
  consumed outside an interaction payload.
- General-logging member join/leave events: the privileged `Guild Members`
  intent.
- Tickets driven by interactions: no additional privileged intent by itself.

The current binary still requests the legacy broad integer mask `3276543`,
which includes privileged intents. Production applications using that binary
must enable every privileged intent it requests in the Discord developer
portal or Discord may reject the gateway session. Reducing this mask to the
minimum enabled feature set remains tracked work; `Guild Presences` is not a v5
product requirement.

## Config Loading Rules

`config.Load()` uses Viper to decode the process environment. In development it
also reads `.env`, with process variables taking precedence. Discord credentials
always use the same `DISCORD_*` names in every environment.

Relevant files:

- `apps/backend/internal/config/config.go`
- `structs/config.go`
- `.env.example`
- `compose.yaml`
- `apps/backend/internal/httpapi/server.go`
- `apps/backend/internal/store/connect_db.go`
- `apps/backend/internal/store/connect_redis.go`
