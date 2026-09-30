# Lumni

Lumni gives a school one place to see **its whole community, and what everyone is doing now**, and turns that network into **modern, school-branded giving**, from a $100 gift to a seven-figure one.

Built for independent schools like the LA-area PreK–12s, where current parents give most and alumni are young, and for universities, where alumni are the whole game.

This repo is the concept build: a Go server (standard library only, in-memory store) with a server-rendered web UI and a JSON API under `/api`.

## What it does

**The community, in one place**
- People of every kind: alumni, current parents, alumni parents, grandparents, faculty, friends. Alumni carry a class year; parents carry their child's grade.
- What each person does now: title, employer, industry, location, bio.
- Search and filter by text, kind, industry, location, class year or grade. Export any view to CSV.
- **Profile links**: every person has a private `/me/…` link the school can text or email so they update their own details in thirty seconds.

**Giving that fits how schools actually raise money**
- **Share-a-link giving** (`/give/…`): one screen, name, email, any amount. If the email matches someone in the directory, the gift is linked to them automatically, so the school knows which alumni and parents gave with no logins.
- **Every way people give**: card, appreciated stock, donor-advised fund grants, monthly, IRA qualified charitable distributions, check and wire. Non-card gifts are recorded as pending until the transfer lands. Donors can ask Lumni to check for an employer match. Every gift gets a tax receipt.
- **Major gifts** ($10K+) are flagged on the dashboard for personal follow-up by the Head of School.
- **Campaigns** with a story, goal, optional deadline, and live progress.
- **Giving Days**: a timed campaign with **challenges** ("first 5 gifts unlock $25K from the Whitfields", "10 alumni gifts unlock $50K", "Grade 7 reaching $20K unlocks $20K"), **sponsor matching**, and live-updating totals.
- **Participation by grade and by class**, the metric independent schools care most about.
- A **donor wall** that respects anonymity, campaign **updates**, and a full **gifts ledger** with CSV export.

**Business model**: no subscription. Lumni takes 4% of each gift (`lumni.PlatformFeeBps`); the dashboard shows what that adds up to.

## Run it

```sh
go run ./cmd/lumni -seed          # :8080 with demo data (or set LUMNI_ADDR)
go test -race ./...
```

Then open http://localhost:8080. The demo school ("Canyon Ridge") is fictional but shaped like a real LA independent school: first senior class in 2009, a $1M/24-hour giving day that's live right now, and a $24M arts-center campaign.

## Pages

| Page | For | What it shows |
|---|---|---|
| `/s/{school}` | school | Dashboard: community size, raised, parent & alumni participation, major gifts to follow up, campaigns with their share links, launch form, who gives by kind, recent gifts |
| `/s/{school}/people` | school | The directory, with filters, CSV export, and add form |
| `/s/{school}/gifts` | school | Every gift, unredacted, with CSV export |
| `/c/{campaign}` | everyone | Campaign page: progress, match, challenges, donor wall, participation, updates, admin panel |
| `/give/{campaign}` | donors | The one-screen give page. Supports `?amount=` and `?email=` presets for links sent to specific people |
| `/me/{member}` | one person | Update your own profile |

Server-rendered `html/template`, one stylesheet (dark, monochrome, one accent), a few lines of JS for live totals.

## API

All routes are prefixed with `/api`. Money is integer cents.

| Method | Path | Description |
|---|---|---|
| GET | `/healthz` | Health check |
| POST / GET | `/schools` | Create or list schools |
| GET | `/schools/{id}` | Get a school |
| GET | `/schools/{id}/insights` | Community and giving analytics |
| POST / GET | `/schools/{id}/members` | Add a person, or search (`?q=&kind=&industry=&location=&grad_year=&grade=`) |
| GET / PATCH | `/schools/{id}/members/{memberID}` | View or update a profile |
| POST / GET | `/schools/{id}/campaigns` | Create or list campaigns (`kind`: `campaign` or `giving_day`; giving days need `starts_at` and `deadline`) |
| GET | `/campaigns/{id}` | Campaign with progress |
| GET | `/campaigns/{id}/live` | Small payload the page polls: totals, challenges, participation |
| POST | `/campaigns/{id}/donations` | Give (`amount_cents`, plus `member_id`, `donor_email` or `donor_name`; `method` card/stock/daf/monthly/ira/check/wire; `employer_match`, `message`, `anonymous`) |
| GET | `/campaigns/{id}/donations` | Donor wall, newest first |
| GET | `/campaigns/{id}/leaderboard?by=class\|grade` | Participation and dollars by class year or grade |
| POST | `/campaigns/{id}/challenges` | Add a challenge |
| POST | `/campaigns/{id}/updates` | Post a progress update |
| POST | `/campaigns/{id}/close` | Stop accepting gifts |

## Layout

```
cmd/lumni/        server entrypoint and demo seed data
internal/lumni/   domain types and validation
internal/store/   in-memory store (matching, challenges, leaderboards, insights)
internal/api/     JSON API handlers
internal/web/     HTML pages, templates and stylesheet
```

## Alumni enrichment (the wedge)

Most schools have a list of graduates and little else. The plan: the school hands Lumni its names (a spreadsheet, or "Class of 2012"), and Lumni comes back with where each person is now, what they do, and how to reach them, then loads them into the directory.

- Sources: licensed professional-data providers (e.g. People Data Labs, Clearbit-style APIs), public profiles, press. No scraping of sites whose terms forbid it.
- Each match carries a confidence score and its sources. Anything under ~90% gets a human check before the school sees it.
- Output lands as members in the directory with `title`, `employer`, `industry`, `location` and an email flagged verified or unverified, plus the `/me/…` link so the person can correct it themselves.
- Not built in this repo yet; it is a service around the store, not a change to it. The interactive demo shows the intended flow on its "Find" screen.

## Next steps
- Alumni enrichment service (above), starting with a CSV in / CSV out pilot for one class year

- Real payments (Stripe) with receipts, so a pilot can take real money
- Logins and roles, so school-only pages and admin actions are protected
- Persistent storage (Postgres) behind the store
- CSV import from Blackbaud / Veracross exports
- Email and SMS sends of give links and profile links from inside the app
