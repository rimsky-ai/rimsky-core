# Rimsky public-surface vocabulary discipline

Three rules govern naming on the public-documentation surface.

## 1. One concept, one name

Every concept has exactly one canonical name. Synonyms are forbidden. Where historical synonyms exist, they are listed below as deprecated, with the current term and the rationale for the change.

### Deprecated terms

| Deprecated | Current | Rationale |
|---|---|---|
| `template_id` | `template_hash` | Templates are content-addressed; `_hash` makes the addressing scheme explicit. | <!-- vocabulary-lint-ignore: template_id -->
| `consumer_key` | `instance_key` | The optional dedup key on an instance is an instance-level concept, not consumer-level. | <!-- vocabulary-lint-ignore: consumer_key -->
| `substrate` | `store` (bundled-services colloquialism) or `claim producer` (protocol-level) per context | "Substrate" conflated the underlying physical storage with the rimsky-side service that wraps it. | <!-- vocabulary-lint-ignore: substrate -->
| `recalculate` (as a graph-level message) | `invalidate` (the only graph-level message) plus the scheduler verb "recalculate" | Rimsky has one cascade message: `invalidate`. The scheduler then recalculates eligible nodes — that's an action of the scheduler, not a peer message. The verb "recalculate" remains correct prose for what the scheduler does; what is deprecated is treating it as a second message type. |

The grep-enforced subset of this list lives at `.vocabulary-lint.yml` (run `go run ./cmd/rimsky-docs-lint vocabulary` to check). Additional forbidden terms get added as concept-file fill surfaces them; each addition specifies a concrete grep pattern.

## 2. One name, one concept (with disambiguation where needed)

A small number of Rimsky terms have more than one valid usage:

- **"Store"** is *not used* in protocol-level prose (use "claim producer"). Among reference impls and operator parlance, "store" is the colloquial name for a data-backed claim producer (filesystem store, postgres store, stub store).
- **"Frame"** is the unit of cascade resolution (per-instance). Internally Rimsky also stamps a frame ID on per-run records as a correlation token, but consumers see the resolution-unit sense.
- **"Recalculate"** is a verb describing what the scheduler does to a `stale` node. It is *not* a graph-level message; the only graph-level message is `invalidate`.

<!-- vocabulary-lint-ignore: region -->
Where a Rimsky term overlaps with general programming vocabulary ("frame", "cascade", "claim", "store", "region"), the concept file's "Common mistakes" section disambiguates the Rimsky meaning from neighboring meanings (stack frame, CSS cascade, JWT claim, Redux store, AWS region).

## 3. Anchors

Every canonical concept file declares up to three anchors in its frontmatter:

- `proto_symbol` — the proto symbol (message, enum, or service) that carries the concept on the wire (under `protocols/proto/v1/`). `(none)` if the concept does not appear on the wire.
- `config_field` — the path inside `rimsky.yml` (operator config) where the concept surfaces. `(none)` if not.
- `api_surface` — the control-api HTTP route where the concept surfaces. `(none)` if not.

Internal anchors (Go types, SQL tables, foundation-side mechanisms) are deliberately *not* part of the public-surface vocabulary. Consumer-visible properties go in the prose "Consumer-visible guarantees" section of the relevant concept file.
