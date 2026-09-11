# Quack v5

Quack is a moderation system for Discord. Administrators define templates with
escalation levels; moderators apply a template to a member; Quack selects the
matching level from that member's history, records an immutable case, and
carries out the level's action through a durable, lease-fenced work queue.

The Discord bot and the dashboard HTTP API are thin adapters over one
application core. Discord is the authority for who may do what: every
protected request re-checks live guild membership and permissions.

## Layout

```
apps/backend/            Go module (github.com/quackdiscord/bot)
  cmd/quack              the single production binary
  cmd/quack-migrate      operator schema adoption, legacy replay, rollback
  cmd/quack-v4-import    import historical v4 cases without affecting escalation
  cmd/quack-storage-verify  backup/restore manifest checks
  internal/quack         application core: use cases, ports, transport-neutral responses
  internal/quack/model   domain types shared by the core and the store
  internal/store         MySQL (GORM) and Redis implementation of the core's ports
  internal/workqueue     bounded in-memory queue over durable action rows
  internal/httpapi       gin routes, middleware, security platform
  internal/discordbot    discordgo session, commands, interactions, rendering
  internal/modules       optional vertical slices: tickets, general logging, honeypots
  internal/moduleintegration  wiring and Discord/HTTP glue for the optional modules
  internal/runtime       the composition root
apps/dashboard/          reserved for the dashboard; empty
contracts/http/openapi.yaml  generated HTTP contract (scripts/generate-openapi.sh)
docs/                    maintainer docs; docs/planning holds product definition and history
```

Start with [`docs/README.md`](docs/README.md), then
[`docs/architecture.md`](docs/architecture.md) and
[`docs/codebase-map.md`](docs/codebase-map.md).

## Development

Copy `.env.example` to `.env`, start MySQL and Redis with
`docker compose up -d`, then run:

```sh
go run ./apps/backend/cmd/quack
```

Validate from `apps/backend`:

```sh
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
QUACK_TEST_MYSQL_DSN='quack:quack@tcp(127.0.0.1:3306)/quack?parseTime=True' go test ./...   # runs the MySQL-gated tests
```

Regenerate the HTTP contract with `./scripts/generate-openapi.sh`.

## Product definition

The backend was built against [`docs/planning/v5.md`](docs/planning/v5.md).
That document and the rest of `docs/planning` are kept for provenance; when
they disagree with the code, the code and the reference docs win.
