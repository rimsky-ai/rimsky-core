// Package executor provides the supervisor-side executor client and
// name→endpoint resolver. Executors are peer services; rimsky never runs
// an executor in-process (except test stubs). Templates reference
// executors by name; the supervisor config maps names to endpoints.
package executor

import (
	"sync"
)

type Endpoint struct {
	Transport string // "grpc" | "http"
	URL       string
	TLS       string // "off" | "optional" | "required"
}

type Resolver interface {
	Resolve(name string) (Endpoint, bool)
	// AcceptedNames returns all configured executor names. Used as the
	// supervisor's accept list when claiming dispatch rows.
	AcceptedNames() []string
}

// StaticResolver is backed by a map set at supervisor startup.
type StaticResolver struct {
	mu sync.RWMutex
	m  map[string]Endpoint
}

func NewStaticResolver(m map[string]Endpoint) *StaticResolver {
	cp := make(map[string]Endpoint, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return &StaticResolver{m: cp}
}

func (r *StaticResolver) Resolve(name string) (Endpoint, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.m[name]
	return e, ok
}

func (r *StaticResolver) AcceptedNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for n := range r.m {
		out = append(out, n)
	}
	return out
}
