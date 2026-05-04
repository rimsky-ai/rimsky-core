package observability

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// handshakeTimeout bounds each per-peer probe at startup. Per spec §4
// the observability handshake is best-effort, so this never aborts
// control-api startup.
const handshakeTimeout = 30 * time.Second

// Prober is the seam for testing the handshake without dialing real
// peers. The default implementation (gRPCProber) dials the supplied
// endpoint and runs both the executor and store capability RPCs.
type Prober interface {
	ProbeExecutor(ctx context.Context, endpoint string) (*ObservabilityCapabilities, error)
	ProbeStore(ctx context.Context, endpoint string) (*ObservabilityCapabilities, error)
}

// gRPCProber is the default Prober. Always dials with insecure
// transport — the operator-configured perimeter handles auth in v1.
type gRPCProber struct{}

// NewGRPCProber returns the production Prober.
func NewGRPCProber() Prober { return gRPCProber{} }

func (gRPCProber) ProbeExecutor(ctx context.Context, endpoint string) (*ObservabilityCapabilities, error) {
	conn, err := dial(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	c := genv1.NewExecutorObservabilityClient(conn)
	resp, err := c.GetCapabilities(ctx, &genv1.GetCapabilitiesRequest{})
	if err != nil {
		return nil, err
	}
	return executorCapsFromProto(resp), nil
}

func (gRPCProber) ProbeStore(ctx context.Context, endpoint string) (*ObservabilityCapabilities, error) {
	conn, err := dial(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	c := genv1.NewStoreObservabilityClient(conn)
	resp, err := c.GetCapabilities(ctx, &genv1.GetStoreCapabilitiesRequest{})
	if err != nil {
		return nil, err
	}
	return storeCapsFromProto(resp), nil
}

func executorCapsFromProto(r *genv1.ObservabilityCapabilities) *ObservabilityCapabilities {
	if r == nil {
		return nil
	}
	return &ObservabilityCapabilities{
		SupportsTraceGet:              r.GetSupportsTraceGet(),
		SupportsTraceStream:           r.GetSupportsTraceStream(),
		RetentionAfterTerminalSeconds: r.GetRetentionAfterTerminalSeconds(),
		CustomUI:                      customUIFromProto(r.GetCustomUi()),
		HTTPBridgeURL:                 r.GetHttpBridgeUrl(),
	}
}

func storeCapsFromProto(r *genv1.StoreObservabilityCapabilities) *ObservabilityCapabilities {
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

// dial opens an insecure gRPC client connection to endpoint. Strips a
// leading grpc:// scheme prefix if present (rimsky.yml store endpoints
// historically use grpc://host:port). Uses NewClient (the post-1.65
// supported call) — the connection is lazy, so a Capabilities() call
// (which is bound by ctx) is the actual reachability gate. The ctx
// argument is reserved for future blocking-dial wrappers and is not
// honored by NewClient itself.
func dial(_ context.Context, endpoint string) (*grpc.ClientConn, error) {
	target := stripScheme(endpoint)
	return grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
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

// PeerSpec is one row from rimsky.yml's executors: or stores: blocks,
// projected for the observability handshake. config.StartControlAPI
// builds a slice of these from RimskyConfig and passes them in,
// keeping this package's import graph free of core/config.
type PeerSpec struct {
	Name                  string
	Endpoint              string
	ObservabilityEndpoint string
}

// RunHandshake probes each declared executor and store's observability
// endpoint at startup, populating the returned Discovery cache. Per
// spec §4 this is best-effort: unreachable peers or absent endpoints
// are recorded as Unreachable and never cause the function to return
// an error.
func RunHandshake(ctx context.Context, prober Prober, executors, stores []PeerSpec, log *slog.Logger) *Discovery {
	if log == nil {
		log = slog.Default()
	}
	d := NewDiscovery(prober)
	for _, e := range executors {
		probe := chooseObsEndpoint(e.ObservabilityEndpoint, e.Endpoint)
		entry := PeerEntry{
			Name:                  e.Name,
			Endpoint:              e.Endpoint,
			ObservabilityEndpoint: probe,
			LastProbedAt:          time.Now(),
		}
		probeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
		caps, err := prober.ProbeExecutor(probeCtx, probe)
		cancel()
		if err != nil {
			entry.Reachability = ReachabilityUnreachable
			entry.LastError = err.Error()
			log.Info("observability.handshake.executor.unreachable",
				slog.String("name", e.Name),
				slog.String("endpoint", probe),
				slog.String("error", err.Error()))
		} else {
			entry.Reachability = ReachabilityReachable
			entry.Capabilities = caps
			if caps != nil {
				entry.HTTPBridgeURL = caps.HTTPBridgeURL
			}
		}
		d.SetExecutor(entry)
	}
	for _, s := range stores {
		probe := chooseObsEndpoint(s.ObservabilityEndpoint, s.Endpoint)
		entry := PeerEntry{
			Name:                  s.Name,
			Endpoint:              s.Endpoint,
			ObservabilityEndpoint: probe,
			LastProbedAt:          time.Now(),
		}
		probeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
		caps, err := prober.ProbeStore(probeCtx, probe)
		cancel()
		if err != nil {
			entry.Reachability = ReachabilityUnreachable
			entry.LastError = err.Error()
			log.Info("observability.handshake.store.unreachable",
				slog.String("name", s.Name),
				slog.String("endpoint", probe),
				slog.String("error", err.Error()))
		} else {
			entry.Reachability = ReachabilityReachable
			entry.Capabilities = caps
			if caps != nil {
				entry.HTTPBridgeURL = caps.HTTPBridgeURL
			}
		}
		d.SetStore(entry)
	}
	return d
}

// RefreshLoop re-probes every peer at the given interval. Heals
// transient unreachability per spec §4. Returns when ctx is cancelled.
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
	for _, e := range d.ListExecutors() {
		probeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
		caps, err := d.prober.ProbeExecutor(probeCtx, e.ObservabilityEndpoint)
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
	}
	for _, e := range d.ListStores() {
		probeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
		caps, err := d.prober.ProbeStore(probeCtx, e.ObservabilityEndpoint)
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
		d.SetStore(updated)
	}
	log.Debug("observability.handshake.refresh",
		slog.Int("executors", len(d.ListExecutors())),
		slog.Int("stores", len(d.ListStores())))
}

func chooseObsEndpoint(observability, fallback string) string {
	if observability != "" {
		return observability
	}
	return fallback
}
