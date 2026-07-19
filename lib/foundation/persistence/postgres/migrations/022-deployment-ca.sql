-- Copyright © 2026 Fall Guy Consulting.
-- Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
-- license. See LICENSE.agpl and COPYRIGHT at the repo root.
CREATE TABLE rimsky_deployment_ca (
    id                UUID        NOT NULL PRIMARY KEY,
    ca_cert_pem       BYTEA       NOT NULL,
    ca_key_encrypted  BYTEA       NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL
);
