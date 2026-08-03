// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package observability

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

const handshakeTimeout = 30 * time.Second

type Prober interface {
	ProbeExecutor(ctx context.Context, peerName, endpoint, tlsMode string) (*ObservabilityCapabilities, error)
	ProbeClaimProducer(ctx context.Context, peerName, endpoint, tlsMode string) (*ObservabilityCapabilities, error)
	ProbeClaimProducerDeclaredErrorClasses(ctx context.Context, peerName, endpoint, tlsMode string) ([]string, error)
}

type gRPCProber struct{}

func NewGRPCProber() Prober { return gRPCProber{} }

func (gRPCProber) ProbeExecutor(ctx context.Context, peerName, endpoint, tlsMode string) (*ObservabilityCapabilities, error) {
	conn, err := dial(peerName, endpoint, tlsMode)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	c := genv1.NewExecutorObservabilityClient(conn)
	resp, err := c.Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		return nil, err
	}
	return executorCapsFromProto(resp), nil
}

func (gRPCProber) ProbeClaimProducer(ctx context.Context, peerName, endpoint, tlsMode string) (*ObservabilityCapabilities, error) {
	conn, err := dial(peerName, endpoint, tlsMode)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	c := genv1.NewClaimProducerObservabilityClient(conn)
	resp, err := c.Capabilities(ctx, &genv1.GetClaimProducerCapabilitiesRequest{})
	if err != nil {
		return nil, err
	}
	return storeCapsFromProto(resp), nil
}

func (gRPCProber) ProbeClaimProducerDeclaredErrorClasses(ctx context.Context, peerName, endpoint, tlsMode string) ([]string, error) {
	conn, err := dial(peerName, endpoint, tlsMode)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	c := genv1.NewClaimProducerClient(conn)
	resp, err := c.Capabilities(ctx, &genv1.CapabilitiesRequest{})
	if err != nil {
		return nil, err
	}
	return append([]string(nil), resp.GetDeclaredErrorClasses()...), nil
}

func executorCapsFromProto(r *genv1.ObservabilityCapabilities) *ObservabilityCapabilities {
	if r == nil {
		return nil
	}
	var schema []byte
	if rs := r.GetExpectedAttributesSchema(); len(rs) > 0 {
		schema = append([]byte(nil), rs...)
	}
	return &ObservabilityCapabilities{
		SupportsTraceGet:              r.GetSupportsTraceGet(),
		SupportsTraceStream:           r.GetSupportsTraceStream(),
		RetentionAfterTerminalSeconds: r.GetRetentionAfterTerminalSeconds(),
		CustomUI:                      customUIFromProto(r.GetCustomUi()),
		HTTPBridgeURL:                 r.GetHttpBridgeUrl(),
		ExpectedAttributesSchema:      schema,
		DeclaredTags:                  append([]string(nil), r.GetDeclaredTags()...),
		DeclaredErrorClasses:          append([]string(nil), r.GetDeclaredErrorClasses()...),
	}
}

func storeCapsFromProto(r *genv1.ClaimProducerObservabilityCapabilities) *ObservabilityCapabilities {
	if r == nil {
		return nil
	}
	out := &ObservabilityCapabilities{
		SupportsClaimGet:              r.GetSupportsClaimGet(),
		SupportsClaimStream:           r.GetSupportsClaimStream(),
		SupportsListClaims:            r.GetSupportsListClaims(),
		RetentionAfterTerminalSeconds: r.GetRetentionAfterTerminalSeconds(),
		CustomUI:                      customUIFromProto(r.GetCustomUi()),
		HTTPBridgeURL:                 r.GetHttpBridgeUrl(),
	}
	for _, v := range r.GetAdminViews() {
		decl := AdminViewDecl{
			Name:        v.GetName(),
			Title:       v.GetTitle(),
			Description: v.GetDescription(),
		}
		for _, p := range v.GetParams() {
			decl.Params = append(decl.Params, AdminViewParam{
				Name:        p.GetName(),
				Type:        p.GetType(),
				Description: p.GetDescription(),
				Required:    p.GetRequired(),
			})
		}
		out.AdminViews = append(out.AdminViews, decl)
	}
	return out
}

func customUIFromProto(r *genv1.CustomUI) *CustomUI {
	if r == nil || r.GetUiUrl() == "" {
		return nil
	}
	return &CustomUI{
		URL:                 r.GetUiUrl(),
		EmbedMode:           r.GetEmbedMode().String(),
		DispatchURLTemplate: r.GetDispatchUrlTemplate(),
	}
}

func dial(peerName, endpoint, tlsMode string) (*grpc.ClientConn, error) {
	target := stripScheme(endpoint)
	label := peerName
	if label == "" {
		label = endpoint
	} else {
		label = peerName + " (" + endpoint + ")"
	}
	return grpc.NewClient(target,
		grpc.WithTransportCredentials(peer.TransportCredentials(tlsMode)),
		grpc.WithUnaryInterceptor(peer.TLSModeUnaryInterceptor(label, tlsMode)),
		grpc.WithStreamInterceptor(peer.TLSModeStreamInterceptor(label, tlsMode)),
	)
}

func stripScheme(s string) string {
	for _, prefix := range []string{"grpc://", "http://", "https://"} {
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			return s[len(prefix):]
		}
	}
	return s
}

type PeerSpec struct {
	Name                  string
	Endpoint              string
	ObservabilityEndpoint string
	TLS                   string
}

func RunHandshake(ctx context.Context, prober Prober, executors, claimProducers []PeerSpec, log *slog.Logger) *Discovery {
	if log == nil {
		log = slog.Default()
	}
	d := NewDiscovery(prober)
	var wg sync.WaitGroup
	for _, e := range executors {
		wg.Add(1)
		go func(e PeerSpec) {
			defer wg.Done()
			d.SetExecutor(probeExecutorEntry(ctx, prober, e, log))
		}(e)
	}
	for _, s := range claimProducers {
		wg.Add(1)
		go func(s PeerSpec) {
			defer wg.Done()
			d.SetClaimProducer(probeClaimProducerEntry(ctx, prober, s, log))
		}(s)
	}
	wg.Wait()
	return d
}

func probeExecutorEntry(ctx context.Context, prober Prober, e PeerSpec, log *slog.Logger) PeerEntry {
	probe := chooseObsEndpoint(e.ObservabilityEndpoint, e.Endpoint)
	entry := PeerEntry{
		Name:                  e.Name,
		Endpoint:              e.Endpoint,
		ObservabilityEndpoint: probe,
		TLS:                   e.TLS,
		LastProbedAt:          time.Now(),
	}
	probeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	caps, err := prober.ProbeExecutor(probeCtx, e.Name, probe, e.TLS)
	cancel()
	if err != nil {
		entry.Reachability = ReachabilityUnreachable
		entry.LastError = err.Error()
		log.Info("observability.handshake.executor.unreachable",
			slog.String("name", e.Name),
			slog.String("endpoint", probe),
			slog.String("error", err.Error()))
		return entry
	}
	entry.Reachability = ReachabilityReachable
	entry.Capabilities = caps
	if caps != nil {
		entry.HTTPBridgeURL = caps.HTTPBridgeURL
	}
	return entry
}

// @decision: producer-declared-classes-capability
func probeClaimProducerEntry(ctx context.Context, prober Prober, s PeerSpec, log *slog.Logger) PeerEntry {
	probe := chooseObsEndpoint(s.ObservabilityEndpoint, s.Endpoint)
	entry := PeerEntry{
		Name:                  s.Name,
		Endpoint:              s.Endpoint,
		ObservabilityEndpoint: probe,
		TLS:                   s.TLS,
		LastProbedAt:          time.Now(),
	}
	probeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	caps, err := prober.ProbeClaimProducer(probeCtx, s.Name, probe, s.TLS)
	cancel()
	classCtx, classCancel := context.WithTimeout(ctx, handshakeTimeout)
	declaredClasses, classErr := prober.ProbeClaimProducerDeclaredErrorClasses(classCtx, s.Name, s.Endpoint, s.TLS)
	classCancel()
	if classErr != nil {
		log.Info("observability.handshake.claim_producer.declared_error_classes.unavailable",
			slog.String("name", s.Name),
			slog.String("endpoint", s.Endpoint),
			slog.String("error", classErr.Error()))
	}
	if err != nil {
		entry.Reachability = ReachabilityUnreachable
		entry.LastError = err.Error()
		log.Info("observability.handshake.claim_producer.unreachable",
			slog.String("name", s.Name),
			slog.String("endpoint", probe),
			slog.String("error", err.Error()))
		if len(declaredClasses) > 0 {
			entry.Capabilities = &ObservabilityCapabilities{DeclaredErrorClasses: declaredClasses}
		}
		return entry
	}
	entry.Reachability = ReachabilityReachable
	entry.Capabilities = caps
	if caps != nil {
		entry.HTTPBridgeURL = caps.HTTPBridgeURL
	}
	if len(declaredClasses) > 0 {
		if entry.Capabilities == nil {
			entry.Capabilities = &ObservabilityCapabilities{}
		}
		entry.Capabilities.DeclaredErrorClasses = declaredClasses
	}
	return entry
}

func (d *Discovery) Refresh(ctx context.Context, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	d.refreshAll(ctx, log)
}

func (d *Discovery) RefreshLoop(ctx context.Context, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.refreshAll(ctx, log)
		}
	}
}

func (d *Discovery) refreshAll(ctx context.Context, log *slog.Logger) {
	executors := d.ListExecutors()
	claimProducers := d.ListClaimProducers()
	var wg sync.WaitGroup
	for _, e := range executors {
		if e.Static {
			continue
		}
		wg.Add(1)
		go func(e PeerEntry) {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
			caps, err := d.prober.ProbeExecutor(probeCtx, e.Name, e.ObservabilityEndpoint, e.TLS)
			cancel()
			updated := e
			updated.LastProbedAt = time.Now()
			if err != nil {
				updated.Reachability = ReachabilityUnreachable
				updated.LastError = err.Error()
				updated.Capabilities = nil
				updated.HTTPBridgeURL = ""
			} else {
				updated.Reachability = ReachabilityReachable
				updated.Capabilities = caps
				updated.LastError = ""
				if caps != nil {
					updated.HTTPBridgeURL = caps.HTTPBridgeURL
				}
			}
			d.SetExecutor(updated)
		}(e)
	}
	for _, e := range claimProducers {
		if e.Static {
			continue
		}
		wg.Add(1)
		go func(e PeerEntry) {
			defer wg.Done()
			d.SetClaimProducer(probeClaimProducerEntry(ctx, d.prober, PeerSpec{
				Name:                  e.Name,
				Endpoint:              e.Endpoint,
				ObservabilityEndpoint: e.ObservabilityEndpoint,
				TLS:                   e.TLS,
			}, log))
		}(e)
	}
	wg.Wait()
	log.Debug("observability.handshake.refresh",
		slog.Int("executors", len(executors)),
		slog.Int("claim_producers", len(claimProducers)))
}

func chooseObsEndpoint(observability, fallback string) string {
	if observability != "" {
		return observability
	}
	return fallback
}
