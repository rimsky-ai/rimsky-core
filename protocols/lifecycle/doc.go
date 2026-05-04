// Package lifecycle defines the LifecycleSubscriber service protocol.
//
// A LifecycleSubscriber is a service that hooks into Rimsky's
// control-plane lifecycle events: template state transitions and
// instance state transitions. See spec §3 for the wire shape.
//
// Implementer pattern: return nil from any method the binary doesn't
// react to. Binaries that don't react to any event simply don't
// implement the service.
package lifecycle
