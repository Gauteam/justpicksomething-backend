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

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/health` | `{"status":"ok","db":"ok"}`, or `503` if the database is unreachable |
