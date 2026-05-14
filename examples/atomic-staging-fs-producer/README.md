# atomic-staging-fs-producer

A reference ClaimProducer implementing the atomic-staging pattern
(see `docs/agents/examples/atomic-staging.md`) over a POSIX filesystem
substrate. Open creates a per-(scope, claim_id) staging directory;
Commit fires a two-rename atomic swap into the canonical location;
Abandon drops the staging directory; Release is a no-op for `r`
intent.

## Build

```sh
go build ./examples/atomic-staging-fs-producer/cmd
```

## Run

```sh
RIMSKY_ATOMIC_STAGING_ROOT=/var/lib/atomic-staging \
RIMSKY_LISTEN_ADDR=:8090 \
RIMSKY_SWEEP_INTERVAL=5m \
RIMSKY_SWEEP_TTL=24h \
./cmd
```

## Layout

- `cmd/` — gRPC server entry point + sweep loop wiring.
- `server/` — gRPC `ClaimProducerServer` adapter.
- `store/` — four-verb logic over the filesystem.
- `sweep/` — periodic reaper for leaked staging directories.
- `template.yaml` — worked-example template using the producer.

## Conformance

Once the binary is running locally, exercise the rimsky-side protocol
expectations via the bundled conformance probe:

```sh
go run ./cmd/rimsky-claim-producer-conformance \
   --endpoint localhost:8090 --transport grpc
```

## Side-table caveats

The `producer_state.jsonl` side-table is a single-writer-per-root
artifact: `appendEntry` and `removeEntry` both read the full file,
modify, and write back via a `.tmp` + `os.Rename` swap (atomic on the
same filesystem). Concurrency is serialised by the coarse `Store.mu`
mutex, which is sufficient as long as **only one process** operates on
a given `<root>`. Running two instances of the binary against the same
root would race on the side-table writes; the second writer's `readAll`
could see a half-written file (parse error) or stomp the first writer's
update. The reference deployment assumes one producer process per
filesystem root; production deployments should enforce this via
filesystem locks or run a single replica per substrate.

## See also

- `docs/agents/examples/atomic-staging.md` — pattern doc.
- `docs/concepts/claim-producer.md` — protocol surface.
- `docs/concepts/held-subgraph.md` — held-subgraph discipline.
