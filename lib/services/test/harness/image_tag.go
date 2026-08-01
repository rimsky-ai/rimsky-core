// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package harness

import (
	"github.com/rimsky-ai/rimsky-core/test/support/imagetag"
)

func ImageRef(repo string) string {
	return imagetag.Ref(repo)
}
