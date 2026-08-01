-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
CREATE TABLE rimsky_deployment_ca (
    id                UUID        NOT NULL PRIMARY KEY,
    ca_cert_pem       BYTEA       NOT NULL,
    ca_key_encrypted  BYTEA       NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL
);
