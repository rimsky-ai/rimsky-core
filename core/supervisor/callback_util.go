// Small helpers shared by callback.go. Split out to keep the callback file
// focused on the endpoint shape.
package supervisor

import "strconv"

// portToStr is a thin wrapper for strconv.Itoa used by CallbackServer.Start
// when joining host:port. The previous name (`chiItoa`) was misleading —
// this has nothing to do with the chi router.
func portToStr(n int) string { return strconv.Itoa(n) }
