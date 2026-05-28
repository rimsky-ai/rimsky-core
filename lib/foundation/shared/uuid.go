// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package shared

import "github.com/google/uuid"

// UUID is the project-wide alias for github.com/google/uuid.UUID. Aliased here
// so persistence + scheduling + control code can refer to "shared.UUID"
// without each caller pulling in the third-party package directly.
type UUID = uuid.UUID
