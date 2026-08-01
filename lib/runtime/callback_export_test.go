// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package runtime

import "net/http"

func HandleAttributeWritebackForTest(c *CallbackServer) http.HandlerFunc {
	return c.handleAttributeWriteback
}
