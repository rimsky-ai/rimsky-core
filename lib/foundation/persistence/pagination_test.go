// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package persistence

import (
	"net/url"
	"strings"
	"testing"
)

func TestACursorTravelsThroughARawQueryStringUnchanged(t *testing.T) {
	for length := 1; length <= 64; length++ {
		var key strings.Builder
		for i := 0; i < length; i++ {
			key.WriteByte(byte('!' + (i*7+length)%94))
		}
		want := key.String()
		cursor := EncodeKeyCursor(want)

		if bad := strings.IndexAny(cursor, "+/= &#?%"); bad >= 0 {
			t.Fatalf("cursor %q carries %q, which a caller must escape before it reaches a query string", cursor, cursor[bad])
		}
		q, err := url.ParseQuery("cursor=" + cursor)
		if err != nil {
			t.Fatalf("cursor %q did not parse as a raw query string: %v", cursor, err)
		}
		got, err := DecodeKeyCursor(q.Get("cursor"))
		if err != nil {
			t.Fatalf("cursor %q did not decode after the round trip: %v", cursor, err)
		}
		if got != want {
			t.Fatalf("cursor round trip returned %q, want %q", got, want)
		}
	}
}
