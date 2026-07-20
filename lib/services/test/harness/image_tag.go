// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"github.com/rimsky-ai/rimsky-core/test/support/imagetag"
)

func ImageRef(repo string) string {
	return imagetag.Ref(repo)
}
