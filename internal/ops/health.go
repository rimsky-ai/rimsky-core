// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package ops

import "net/http"

// HealthHandler returns an http.Handler that responds 200 OK to GET
// requests; everything else is 405. The body is a tiny JSON object
// (`{"status":"ok"}`) so curl / wget output is friendly.
//
// Callers wanting readiness semantics (versus liveness) pass a
// ready func that returns nil when the service is ready; a non-nil
// return becomes 503 with the error message as the body.
func HealthHandler(ready func() error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ready != nil {
			if err := ready(); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"unready","error":` + jsonString(err.Error()) + `}`))
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
}

// jsonString returns a minimal JSON-safe-quoted version of s. Avoids
// pulling encoding/json just to escape a single string in the rare
// unready path.
func jsonString(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			out = append(out, '\\', byte(r))
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if r < 0x20 {
				continue
			}
			out = append(out, []byte(string(r))...)
		}
	}
	out = append(out, '"')
	return string(out)
}
