// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package shared

import "github.com/google/uuid"

// UUID is the project-wide alias for github.com/google/uuid.UUID. Aliased here
// so persistence + scheduling + control code can refer to "shared.UUID"
// without each caller pulling in the third-party package directly.
type UUID = uuid.UUID
