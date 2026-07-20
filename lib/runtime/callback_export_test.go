// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import "net/http"

func HandleAttributeWritebackForTest(c *CallbackServer) http.HandlerFunc {
	return c.handleAttributeWriteback
}
