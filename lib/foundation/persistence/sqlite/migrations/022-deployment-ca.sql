-- Copyright © 2026 Fall Guy Consulting.
-- SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial
CREATE TABLE rimsky_deployment_ca (
    id                TEXT NOT NULL PRIMARY KEY,
    ca_cert_pem       BLOB NOT NULL,
    ca_key_encrypted  BLOB NOT NULL,
    created_at        TEXT NOT NULL
);
