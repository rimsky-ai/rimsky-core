// Runner JSON unmarshal seam — split out so tests can swap the
// implementation if needed. See runner.go for the call site.
package supervisor

import "encoding/json"

func jsonUnmarshalImpl(b []byte, v any) error { return json.Unmarshal(b, v) }
