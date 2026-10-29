# justpicksomething-backend

Go API for Just Pick Something, deployed as a single Vercel function at
`https://api.pick.rajwalgautam.com`. Project plan:
[justpicksomething-docs](https://github.com/rajwalgautam/justpicksomething-docs/blob/main/project-plan.md).

## Layout

- `api/index.go` — the Vercel function. `vercel.json` rewrites every path to it.
- `pkg/server` — router and security middleware (HTTPS, HSTS, CORS, origin check).
- `pkg/db` — Neon connection pool (`DATABASE_URL`).
- `cmd/dev` — local server for development.
- `migrations` — SQL migrations for [golang-migrate](https://github.com/golang-migrate/migrate).

## Local development

Requires Go 1.25+ and the `migrate` CLI.

```bash
cp .env.example .env   # fill in the Neon dev branch connection strings
make migrate-up
make dev               # http://localhost:8080/health
make test
```

## Environment variables

| Name | Where | Value |
| --- | --- | --- |
| `DATABASE_URL` | Vercel + local | Neon pooled connection string (`-pooler` host) |
| `MIGRATE_DATABASE_URL` | local only | Neon direct connection string, for migrations |
| `ALLOWED_ORIGINS` | Vercel + local | Comma-separated origins, e.g. `https://pick.rajwalgautam.com` |

On Vercel (`VERCEL` is set) the API also rejects non-HTTPS requests and sends HSTS.

## Endpoints

Authenticated endpoints take `Authorization: Bearer <token>`, where the token
comes from creating or joining the room. A bad or missing token is `401`.

| Method | Path | Auth | Body → Response |
| --- | --- | --- | --- |
| `GET` | `/health` | — | → `{"status":"ok","db":"ok"}`, or `503` if the database is unreachable |
| `POST` | `/rooms` | — | `{name, displayName}` → `201 {slug, passcode, token}`; creator is admin |
| `POST` | `/rooms/{slug}/join` | — | `{passcode, displayName}` → `201 {token}`; `401` wrong passcode, `404` no room, `409` name taken |
| `GET` | `/rooms/{slug}` | ✓ | → `{room, me, members, columns, items}`; items sorted by score with `fields`, `position`, `commentCount`, `up`, `down`, `score`, `myVote`. `?since=<room.updatedAt>` → `{"unchanged":true}` if nothing changed (not `304`: Vercel drops CORS headers on 304s) |
| `POST` | `/rooms/{slug}/items` | ✓ | `{title}` or `{titles: [...]}` (max 50) → `201 {items}`; blanks and titles already in the room (ignoring case) are skipped, so `items` may be shorter or empty |
| `PUT` | `/rooms/{slug}/items/{id}/vote` | ✓ | `{value: 1 \| -1}` → `204` |
| `DELETE` | `/rooms/{slug}/items/{id}/vote` | ✓ | → `204` |
| `PATCH` | `/rooms/{slug}/items/{id}` | ✓ | `{title?, fields?: {columnId: value \| null}}` → `204`; `""` or `null` clears a value; `409` title taken |
| `PUT` | `/rooms/{slug}/items/order` | admin | `{ids: [...]}` (every item, in order) → `204`; `409` if the list is stale |
| `POST` | `/rooms/{slug}/columns` | admin | `{name, type: "text" \| "boolean" \| "textarea"}` → `201` column; `409` name taken or 20 columns |
| `PATCH` | `/rooms/{slug}/columns/{id}` | admin | `{name}` → `204` |
| `DELETE` | `/rooms/{slug}/columns/{id}` | admin | → `204`; also removes its values from every item |
| `PUT` | `/rooms/{slug}/columns/order` | admin | `{ids: [...]}` (every column, in order) → `204` |
| `GET` | `/rooms/{slug}/items/{id}/comments` | ✓ | → `{comments}`, oldest first |
| `POST` | `/rooms/{slug}/items/{id}/comments` | ✓ | `{body}` → `201` comment |
| `PUT` | `/rooms/{slug}/comments/{id}` | author | `{body}` → `204`; marks it edited |
| `DELETE` | `/rooms/{slug}/comments/{id}` | author or admin | → `204` |

Limits: room name 100 characters, display name 40, item title 200, column
name 40, short-text value 200, long-text value 2,000, comment 1,000; 20 columns
per room; 50 items per bulk add. Passcodes are 6 characters, case-insensitive.
