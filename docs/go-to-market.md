# Go-to-Market Notes

Working notes on how to find collaborators and early adopters for Rimsky given that most social-media channels aggressively spam-filter new accounts and new projects.

## Guiding principle

Don't announce the project; contribute technical substance. Decision-makers in infra tooling don't read launches — they read postmortems, benchmarks, design tradeoffs, and integration guides. The project becomes the byline, not the headline.

## Channels that don't punish newness

- **Show HN** — explicitly tolerates new accounts; designed for first-time launches. One real shot per major release. Lead with a specific design choice ("why we chose Postgres-only coordination over a control-plane DB"), not "introducing X."
- **Lobste.rs** — invite-only, but the audience is exactly the senior systems people we want. Worth asking around for an invite.
- **Targeted subreddits** — r/golang, r/devops, r/dataengineering, r/MachineLearning, r/ExperiencedDevs. Posts framed as "we made these tradeoffs differently than Temporal/Airflow/Dagster — here's why" land; "check out my project" gets removed.
- **Conference & meetup CFPs** — KubeCon, SREcon, GopherCon, PyCon, QCon, Strange Loop successors, plus local Go / SRE / data-eng meetups. CFP review doesn't care about account age. A talk puts us in a room of decision-makers and yields a permanent YouTube artifact.
- **Guest posts** — InfoQ, The New Stack, ACM Queue, Increment-style outlets, individual engineers' blogs. Editors actively want practitioner content.
- **Podcasts** — Software Engineering Daily, Changelog, Go Time, MLOps Community, Data Engineering Podcast. Pitch a *topic* (e.g. "decoupled atomicity in distributed orchestration"), not the tool.
- **Direct outreach to maintainers of adjacent tools** — Airflow / Temporal / Prefect / Dagster plugin authors. They know the pain and the buyers. A cold email asking a specific technical question or for a reaction to a design choice is flattering, not spammy.
- **Slack / Discord communities** — MLOps Community Slack (~25k senior practitioners), Gophers Slack, r/dataengineering Discord, DevOps Discord. Participate with substance for a few weeks before mentioning the project.
- **Build integrations** — every Airflow / Temporal / n8n / Dagster bridge ships us into that ecosystem's discovery surface (their docs, their contributors' channels).
- **Trending GitHub** — README quality, a 30-second demo gif/video, and a couple of starred-by-known-people boosts can land us on language-trending pages without spending a cent.

## Where to pay

- **Console.dev** — the highest-signal dev-tool newsletter; sponsorship is cheap relative to audience quality.
- **Bytes.dev, Pointer.io, TLDR DevOps / AI** — sponsor slots in established dev newsletters; recipients self-selected as tool-curious.
- **Reddit ads** targeted to r/devops, r/dataengineering, r/golang — surprisingly cost-effective for dev tools because targeting is precise and the established-account problem doesn't apply to ads.
- **Carbon Ads** — places on sites developers actually visit; aesthetic, non-intrusive, infra-tool-friendly.
- **Conference sponsorship at the small end** — SREcon BoFs, GopherCon community track, regional KubeCons. Cheaper than the marquee tier and the booth conversations are with practitioners, not procurement.
- **GitHub Sponsors of adjacent maintainers** — supports the ecosystem and puts us on their radar; not a marketing channel per se but it builds the relationships that turn into integrations and word-of-mouth.

## What to skip

- Twitter / X and LinkedIn cold posts from new accounts (the original constraint).
- Product Hunt — its audience is consumer / SaaS, not infra.
- Generic press releases or PR firms — zero leverage for dev tools.
- Paid Twitter / X ads for B2B infra — expensive and the audience there is the wrong shape.

## One thing to lead with

If we have one piece of "marketing" budget — money or attention — spend it on **a single deeply technical artifact**: a benchmark vs. Temporal / Airflow on a specific axis, or a postmortem of why an architectural decision worked, or a "build a workflow engine in 200 lines" walkthrough that lands at our design. That artifact is the thing we reference in every Show HN, podcast pitch, CFP, and cold email. Without it, channels don't compound; with it, every channel forwards to the same proof.
