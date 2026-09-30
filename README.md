# Lumni

Lumni gives a school one place to see **all of its alumni and what they're doing now**, and to turn that network into **modern, school-branded fundraising**. Think GoFundMe, built for institutional advancement.

This repo is the first concept: a Go server (standard library only, in-memory store) with a server-rendered web UI and a JSON API under `/api`.

## What it does

**Alumni directory**
- Profiles: grad year, degree, employer, title, industry, location, bio
- Search and filter by text (`q`), `industry`, `location` and `grad_year`
- Insights for the school: headcount by industry, location and class year, top employers, and giving participation rate

**Fundraising campaigns**
- Campaigns with a story, a goal, an optional deadline, and a live `percent_funded`
- **Matching gifts**: a sponsor (for example, "Class of 1990") matches gifts 1:1 up to a cap
- **Donor wall** that respects anonymous gifts
- **Class leaderboard** that ranks graduating classes by giving
- **Campaign updates** so donors can see progress
- Donations can be linked to an alumnus profile, which drives participation stats

## Run it

```sh
go run ./cmd/lumni -seed          # listens on :8080 with demo data (or set LUMNI_ADDR)
go test -race ./...
```

Then open http://localhost:8080.

## Web UI

| Page | What it shows |
|---|---|
| `/` | Schools on the platform; add a school |
| `/s/{school}` | **School dashboard**: alumni count, total raised, participation rate, breakdowns by industry / location / employer, campaign list, launch-a-campaign form |
| `/s/{school}/alumni` | **Alumni directory** with search and filters; add-an-alum form |
| `/c/{campaign}` | **Public campaign page**: story, progress bar, matching-gift banner, donor wall, class leaderboard, updates, and a Give form |

Pages are plain `html/template` with a single stylesheet (light and dark), no JavaScript framework.

## API

All API routes are prefixed with `/api`.

| Method | Path | Description |
|---|---|---|
| GET | `/healthz` | Health check |
| POST / GET | `/schools` | Create or list schools |
| GET | `/schools/{id}` | Get a school |
| GET | `/schools/{id}/insights` | Alumni and giving analytics |
| POST / GET | `/schools/{id}/alumni` | Add an alumnus, or search the directory (`?q=&industry=&location=&grad_year=`) |
| GET / PATCH | `/schools/{id}/alumni/{alumnusID}` | View or update a profile |
| POST / GET | `/schools/{id}/campaigns` | Create or list campaigns |
| GET | `/campaigns/{id}` | Campaign with progress |
| POST | `/campaigns/{id}/donations` | Donate (`amount_cents`, plus `alumnus_id` or `donor_name`, `message`, `anonymous`) |
| GET | `/campaigns/{id}/donations` | Donor wall, newest first |
| GET | `/campaigns/{id}/leaderboard` | Giving ranked by class year |
| POST | `/campaigns/{id}/updates` | Post a progress update (`body`) |
| POST | `/campaigns/{id}/close` | Stop accepting donations |

All money values are integer cents.

Example:

```sh
curl -X POST localhost:8080/api/schools -d '{"name":"Westbrook University"}'
curl -X POST localhost:8080/api/schools/$SCHOOL/campaigns \
  -d '{"title":"New Science Library","goal_cents":5000000,"match":{"sponsor":"Class of 1990","cap_cents":1000000}}'
curl -X POST localhost:8080/api/campaigns/$CAMPAIGN/donations -d '{"alumnus_id":"'$ALUM'","amount_cents":25000}'
```

## Layout

```
cmd/lumni/        server entrypoint and demo seed data
internal/lumni/   domain types and validation
internal/store/   in-memory store (matching, leaderboard, insights)
internal/api/     JSON API handlers (Go 1.22+ method routing)
internal/web/     HTML pages, templates and stylesheet
```

## Next steps

- Persistent storage (Postgres) behind the store interface
- Authentication and roles (school admin vs. alumnus)
- Real payments (Stripe) instead of recorded pledges
- CSV import of existing alumni records
- Logins so the admin actions (launch, update, close) are limited to school staff
