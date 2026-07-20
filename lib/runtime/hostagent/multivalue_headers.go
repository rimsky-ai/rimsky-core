// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// @concept: host-agent
package hostagent

import (
	"net/http"
	"strings"
)

const multiValueHeaderJoin = "\n"

func JoinHeaderValues(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = strings.Join(v, multiValueHeaderJoin)
	}
	return out
}

func ApplyJoinedHeaders(dst http.Header, src map[string]string) {
	for k, v := range src {
		for _, part := range strings.Split(v, multiValueHeaderJoin) {
			dst.Add(k, part)
		}
	}
}
