-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.

-- 008-claim-handle-payload.sql
--
-- Persist the producer-supplied claim payload on the rimsky_claim_handles
-- row. The supervisor receives the payload bytes in ClaimResult at Open
-- time and forwards them to the acquiring node's executor (the
-- {{claim.<alias>.payload.<f>}} substitution + the StoreHandle wire
-- entry). Without persistence the bytes are lost as soon as the acquirer's
-- own dispatch tx commits — a downstream co-holder declaring `holds:`
-- against the alias re-reads the row at its own acquire-tx and finds
-- everything EXCEPT the payload, so its `{{claim.<alias>.payload.<f>}}`
-- substitution drops to ErrMissingSource and the dispatch lands in
-- terminal/error/template_resolution_failed.
--
-- Adding the column closes the gap. The payload bytes are inert in
-- rimsky per `@blessed-invariant 20` (no rimsky code reads into them
-- except the sanctioned introspection sites in
-- `lib/graph/attribute/substitution.go`); the column is therefore a
-- nullable JSONB blob with no CHECK constraint beyond the lock-kind
-- partial validation (payload is meaningful for claim-scope rows only).
--
-- Pre-v1 there is no production data to migrate; the column lands as
-- NULL on every existing row.

ALTER TABLE rimsky_claim_handles
    ADD COLUMN payload JSONB;
