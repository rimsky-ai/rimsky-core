// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pgsstore "github.com/fallguy/rimsky/stores/postgres/store"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// itemsTableIdentRe is the shared strict SQL-identifier regex
// (pgsstore.ItemsTableIdentRegex). Defense-in-depth: even though
// Store.New validates the same shape at config load, the store is
// constructed via NewForTest without validation in unit tests and a
// future loader change could regress — this guard ensures we never
// build a query against a name that could carry SQL.
var itemsTableIdentRe = pgsstore.ItemsTableIdentRegex

// ObservabilityServer is the postgres store's StoreObservability
// implementation. Exposes two admin views: `pick_policies` (declared
// policies + their default actions) and `items_queue` (per-policy
// queued vs in-progress count).
type ObservabilityServer struct {
	genv1.UnimplementedStoreObservabilityServer
	store *pgsstore.Store
	// httpBridgeURL is set once at startup before the gRPC server
	// accepts traffic; sync.Once-style write means later reads can be
	// lock-free. We use sync.Once explicitly to make the contract
	// loud at the call site.
	httpBridgeURLOnce sync.Once
	httpBridgeURL     string
	idleTimeout       time.Duration
}

// NewObservabilityServer pins the observability surface to a live
// postgres store handle.
func NewObservabilityServer(store *pgsstore.Store) *ObservabilityServer {
	return &ObservabilityServer{store: store, idleTimeout: defaultObsIdleTimeout}
}

// SetHTTPBridgeURL records the URL the store advertises in
// StoreObservabilityCapabilities.http_bridge_url. Set-once at startup;
// subsequent calls are ignored. Empty value disables.
func (s *ObservabilityServer) SetHTTPBridgeURL(u string) {
	s.httpBridgeURLOnce.Do(func() { s.httpBridgeURL = u })
}

// SetIdleTimeout overrides the default StreamClaim idle timeout. Pass
// zero for never-timeout behaviour. Must be set before any stream
// starts (set-once-at-startup).
func (s *ObservabilityServer) SetIdleTimeout(d time.Duration) { s.idleTimeout = d }

// defaultObsIdleTimeout is the spec §2.5 / §3.5 default close-idle
// timeout for live observability streams.
const defaultObsIdleTimeout = 5 * time.Minute

// GetCapabilities reports the v1 surface: admin views plus per-claim
// get/stream/list backed by an in-memory ledger.
func (s *ObservabilityServer) GetCapabilities(_ context.Context, _ *genv1.GetStoreCapabilitiesRequest) (*genv1.StoreObservabilityCapabilities, error) {
	return &genv1.StoreObservabilityCapabilities{
		SupportsClaimGet:              true,
		SupportsClaimStream:           true,
		SupportsListClaims:            true,
		RetentionAfterTerminalSeconds: 3600,
		HttpBridgeUrl:                 s.httpBridgeURL,
		AdminViews: []*genv1.AdminViewDecl{
			{Name: "pick_policies", Title: "Pick policies", Description: "Configured pick policies"},
			{Name: "items_queue", Title: "Items queue", Description: "Queued and in-progress counts per pick policy"},
		},
	}, nil
}

// GetClaim returns the recorded claim history. Returns ClaimDetail{state:
// UNKNOWN} when the ledger has evicted the record.
func (s *ObservabilityServer) GetClaim(_ context.Context, req *genv1.GetClaimRequest) (*genv1.ClaimDetail, error) {
	rec, ok := s.store.Ledger().Get(req.GetClaimId())
	if !ok {
		return &genv1.ClaimDetail{ClaimId: req.GetClaimId(), State: genv1.ClaimState_UNKNOWN}, nil
	}
	return claimRecordToDetail(rec), nil
}

// StreamClaim atomically replays the history then streams new events
// until the claim hits a terminal (or the client disconnects, or the
// idle timeout fires per spec §3.5). Subscribers register under the
// ledger's lock so events appended between snapshot and subscribe are
// not lost.
func (s *ObservabilityServer) StreamClaim(req *genv1.StreamClaimRequest, stream genv1.StoreObservability_StreamClaimServer) error {
	history, rec, ch, unsub := s.store.Ledger().SubscribeWithSnapshot(req.GetClaimId())
	defer unsub()
	if rec == nil {
		return stream.Send(&genv1.ClaimEvent{
			EventId:   "evicted",
			Timestamp: timestamppb.Now(),
			Severity:  genv1.Severity_INFO,
			Category:  "claim_terminal",
		})
	}
	for _, ev := range history {
		if err := stream.Send(claimEventToProto(ev)); err != nil {
			return err
		}
	}
	if rec.State != pgsstore.ClaimStateOpen {
		return stream.Send(&genv1.ClaimEvent{
			EventId:   "terminal",
			Timestamp: timestamppb.New(time.Now().UTC()),
			Severity:  genv1.Severity_INFO,
			Category:  "claim_terminal",
		})
	}
	idle := s.idleTimeout
	for {
		var idleC <-chan time.Time
		if idle > 0 {
			t := time.NewTimer(idle)
			defer t.Stop()
			idleC = t.C
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-idleC:
			// Spec §2.5/§3.5: close idle streams with a final marker,
			// not an error.
			return stream.Send(&genv1.ClaimEvent{
				EventId:   "idle_timeout",
				Timestamp: timestamppb.Now(),
				Severity:  genv1.Severity_INFO,
				Category:  "claim_terminal",
			})
		case ev, ok := <-ch:
			if !ok {
				// Channel closed → terminal arrived.
				return stream.Send(&genv1.ClaimEvent{
					EventId:   "terminal",
					Timestamp: timestamppb.New(time.Now().UTC()),
					Severity:  genv1.Severity_INFO,
					Category:  "claim_terminal",
				})
			}
			if err := stream.Send(claimEventToProto(ev)); err != nil {
				return err
			}
		}
	}
}

// ListClaims returns a cursor-paginated view of the in-memory ledger.
func (s *ObservabilityServer) ListClaims(_ context.Context, req *genv1.ListClaimsRequest) (*genv1.ClaimList, error) {
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	recs, next := s.store.Ledger().List(req.GetStateFilter(), req.GetCursor(), limit)
	out := make([]*genv1.ClaimSummary, 0, len(recs))
	for _, rec := range recs {
		summ := &genv1.ClaimSummary{
			ClaimId: rec.ClaimID,
			State:   claimStateToProto(rec.State),
		}
		if !rec.OpenedAt.IsZero() {
			summ.OpenedAt = timestamppb.New(rec.OpenedAt)
		}
		if rec.ClosedAt != nil {
			summ.ClosedAt = timestamppb.New(*rec.ClosedAt)
		}
		out = append(out, summ)
	}
	return &genv1.ClaimList{Claims: out, NextCursor: next}, nil
}

// claimRecordToDetail converts a postgres-store ledger record into
// the wire ClaimDetail. Mirrors the filesystem store's helper of the
// same name; intentional duplication tracked via @source.
//
//	@source: stores/filesystem/server/observability.go:claimRecordToDetail
func claimRecordToDetail(rec *pgsstore.ClaimRecord) *genv1.ClaimDetail {
	d := &genv1.ClaimDetail{
		ClaimId: rec.ClaimID,
		State:   claimStateToProto(rec.State),
	}
	if !rec.OpenedAt.IsZero() {
		d.OpenedAt = timestamppb.New(rec.OpenedAt)
	}
	if rec.ClosedAt != nil {
		d.ClosedAt = timestamppb.New(*rec.ClosedAt)
	}
	if len(rec.Address) > 0 {
		var v any
		if err := json.Unmarshal(rec.Address, &v); err == nil {
			if st, err := structpb.NewStruct(map[string]any{"value": v}); err == nil {
				d.Address = st
			}
		}
	}
	if len(rec.Scope) > 0 {
		var v any
		if err := json.Unmarshal(rec.Scope, &v); err == nil {
			if st, err := structpb.NewStruct(map[string]any{"value": v}); err == nil {
				d.Scope = st
			}
		}
	}
	for _, ev := range rec.History {
		d.History = append(d.History, claimEventToProto(ev))
	}
	return d
}

// claimEventToProto mirrors the filesystem store's helper of the same
// name; intentional duplication tracked via @source.
//
//	@source: stores/filesystem/server/observability.go:claimEventToProto
func claimEventToProto(ev pgsstore.ClaimEvent) *genv1.ClaimEvent {
	out := &genv1.ClaimEvent{
		EventId:   ev.EventID,
		Timestamp: timestamppb.New(ev.Timestamp),
		Severity:  severityFromString(ev.Severity),
		Category:  ev.Category,
		Message:   ev.Message,
	}
	if len(ev.Attributes) > 0 {
		if st, err := structpb.NewStruct(ev.Attributes); err == nil {
			out.Attributes = st
		}
	}
	return out
}

// claimStateToProto mirrors the filesystem store's helper of the same name.
//
//	@source: stores/filesystem/server/observability.go:claimStateToProto
func claimStateToProto(st pgsstore.ClaimState) genv1.ClaimState {
	switch st {
	case pgsstore.ClaimStateOpen:
		return genv1.ClaimState_OPEN
	case pgsstore.ClaimStateCommitted:
		return genv1.ClaimState_COMMITTED
	case pgsstore.ClaimStateAbandoned:
		return genv1.ClaimState_ABANDONED
	case pgsstore.ClaimStateReleased:
		return genv1.ClaimState_RELEASED
	default:
		return genv1.ClaimState_UNKNOWN
	}
}

// severityFromString mirrors the filesystem store's helper.
//
//	@source: stores/filesystem/server/observability.go:severityFromString
func severityFromString(s string) genv1.Severity {
	switch s {
	case "DEBUG":
		return genv1.Severity_DEBUG
	case "WARN":
		return genv1.Severity_WARN
	case "ERROR":
		return genv1.Severity_ERROR
	default:
		return genv1.Severity_INFO
	}
}

// GetAdminView dispatches by name.
func (s *ObservabilityServer) GetAdminView(ctx context.Context, req *genv1.GetAdminViewRequest) (*genv1.AdminView, error) {
	switch req.GetViewName() {
	case "pick_policies":
		return s.pickPoliciesView()
	case "items_queue":
		return s.itemsQueueView(ctx)
	default:
		return nil, status.Errorf(codes.NotFound, "unknown admin view %q", req.GetViewName())
	}
}

func (s *ObservabilityServer) pickPoliciesView() (*genv1.AdminView, error) {
	pps := s.store.PickPolicies()
	selectors := make([]string, 0, len(pps))
	for sel := range pps {
		selectors = append(selectors, sel)
	}
	sort.Strings(selectors)
	rows := make([]any, 0, len(selectors))
	for _, sel := range selectors {
		pp := pps[sel]
		rows = append(rows, map[string]any{
			"selector":                   sel,
			"items_table":                pp.ItemsTable,
			"on_commit":                  string(pp.OnCommit.Kind),
			"on_give_up":                 string(pp.OnGiveUp.Kind),
			"visibility_timeout_seconds": int(pp.VisibilityTimeout.Seconds()),
		})
	}
	data, _ := structpb.NewStruct(map[string]any{"rows": rows})
	return &genv1.AdminView{
		Schema: &genv1.AdminViewSchema{Columns: []*genv1.AdminViewColumn{
			{Name: "selector", Type: "string"},
			{Name: "items_table", Type: "string"},
			{Name: "on_commit", Type: "string"},
			{Name: "on_give_up", Type: "string"},
			{Name: "visibility_timeout_seconds", Type: "int"},
		}},
		Data:       data,
		RenderHint: "table",
	}, nil
}

func (s *ObservabilityServer) itemsQueueView(ctx context.Context) (*genv1.AdminView, error) {
	pool := s.store.Pool()
	if pool == nil {
		return nil, status.Error(codes.FailedPrecondition, "postgres store: pool not initialised")
	}
	pps := s.store.PickPolicies()
	selectors := make([]string, 0, len(pps))
	for sel := range pps {
		selectors = append(selectors, sel)
	}
	sort.Strings(selectors)
	rows := make([]any, 0, len(selectors))
	for _, sel := range selectors {
		pp := pps[sel]
		// Defense-in-depth: reject any items_table name that isn't a
		// strict SQL identifier before interpolating it into the
		// COUNT(*) query. Store.New already enforces this at config
		// load; if it ever fails to, we'd rather return -1/-1 than
		// open a SQL injection.
		if !itemsTableIdentRe.MatchString(pp.ItemsTable) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"postgres store: pick_policies[%q]: items_table %q is not a valid SQL identifier",
				sel, pp.ItemsTable)
		}
		var queued, inProgress int
		queryQ := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE state = 'queued'", pp.ItemsTable)
		queryIP := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE state = 'in_progress'", pp.ItemsTable)
		if err := pool.QueryRow(ctx, queryQ).Scan(&queued); err != nil {
			slog.Warn("postgres-store.itemsQueueView: queued count failed",
				slog.String("selector", sel),
				slog.String("items_table", pp.ItemsTable),
				slog.String("error", err.Error()))
			queued = -1
		}
		if err := pool.QueryRow(ctx, queryIP).Scan(&inProgress); err != nil {
			slog.Warn("postgres-store.itemsQueueView: in-progress count failed",
				slog.String("selector", sel),
				slog.String("items_table", pp.ItemsTable),
				slog.String("error", err.Error()))
			inProgress = -1
		}
		rows = append(rows, map[string]any{
			"selector":          sel,
			"items_table":       pp.ItemsTable,
			"queued_count":      queued,
			"in_progress_count": inProgress,
		})
	}
	data, _ := structpb.NewStruct(map[string]any{"rows": rows})
	return &genv1.AdminView{
		Schema: &genv1.AdminViewSchema{Columns: []*genv1.AdminViewColumn{
			{Name: "selector", Type: "string"},
			{Name: "items_table", Type: "string"},
			{Name: "queued_count", Type: "int"},
			{Name: "in_progress_count", Type: "int"},
		}},
		Data:       data,
		RenderHint: "table",
	}, nil
}

// RegisterObservability registers the observability server alongside
// the existing StoreService server. Returns the constructed
// ObservabilityServer so callers can mount the HTTP+JSON bridge and
// wire SetHTTPBridgeURL.
func (s *Server) RegisterObservability(grpcSrv *grpc.Server) *ObservabilityServer {
	o := NewObservabilityServer(s.store)
	genv1.RegisterStoreObservabilityServer(grpcSrv, o)
	return o
}
