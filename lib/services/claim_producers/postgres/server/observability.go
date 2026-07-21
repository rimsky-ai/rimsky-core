// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

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

	pgsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/postgres/store"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

var itemsTableIdentRe = pgsstore.ItemsTableIdentRegex

type ObservabilityServer struct {
	genv1.UnimplementedClaimProducerObservabilityServer
	store             *pgsstore.Store
	httpBridgeURLOnce sync.Once
	httpBridgeURL     string
	idleTimeout       time.Duration
}

func NewObservabilityServer(store *pgsstore.Store) *ObservabilityServer {
	return &ObservabilityServer{store: store, idleTimeout: defaultObsIdleTimeout}
}

func (s *ObservabilityServer) SetHTTPBridgeURL(u string) {
	s.httpBridgeURLOnce.Do(func() { s.httpBridgeURL = u })
}

func (s *ObservabilityServer) SetIdleTimeout(d time.Duration) { s.idleTimeout = d }

const defaultObsIdleTimeout = 5 * time.Minute

func (s *ObservabilityServer) Capabilities(_ context.Context, _ *genv1.GetClaimProducerCapabilitiesRequest) (*genv1.ClaimProducerObservabilityCapabilities, error) {
	return &genv1.ClaimProducerObservabilityCapabilities{
		SupportsClaimGet:              true,
		SupportsClaimStream:           true,
		SupportsListClaims:            true,
		RetentionAfterTerminalSeconds: 0,
		HttpBridgeUrl:                 s.httpBridgeURL,
		AdminViews: []*genv1.AdminViewDecl{
			{Name: "pick_policies", Title: "Pick policies", Description: "Configured pick policies"},
			{Name: "items_queue", Title: "Items queue", Description: "Queued and in-progress counts per pick policy"},
		},
	}, nil
}

func (s *ObservabilityServer) GetClaim(_ context.Context, req *genv1.GetClaimRequest) (*genv1.ClaimDetail, error) {
	rec, ok := s.store.Ledger().Get(req.GetClaimId())
	if !ok {
		return &genv1.ClaimDetail{ClaimId: req.GetClaimId(), State: genv1.ClaimState_UNKNOWN}, nil
	}
	return claimRecordToDetail(rec), nil
}

func (s *ObservabilityServer) StreamClaim(req *genv1.StreamClaimRequest, stream genv1.ClaimProducerObservability_StreamClaimServer) error {
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
	var idleTimer *time.Timer
	var idleC <-chan time.Time
	if idle > 0 {
		idleTimer = time.NewTimer(idle)
		defer idleTimer.Stop()
		idleC = idleTimer.C
	}
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-idleC:
			return stream.Send(&genv1.ClaimEvent{
				EventId:   "idle_timeout",
				Timestamp: timestamppb.Now(),
				Severity:  genv1.Severity_INFO,
				Category:  "claim_terminal",
			})
		case ev, ok := <-ch:
			if !ok {
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
			if idleTimer != nil {
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(idle)
			}
		}
	}
}

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
		if !itemsTableIdentRe.MatchString(pp.ItemsTable) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"postgres store: pick_policies[%q]: items_table %q is not a valid SQL identifier",
				sel, pp.ItemsTable)
		}
		var queued, inProgress int
		queryQ := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE state = 'available'", pp.ItemsTable)
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

func (s *Server) RegisterObservability(grpcSrv *grpc.Server) *ObservabilityServer {
	o := NewObservabilityServer(s.store)
	genv1.RegisterClaimProducerObservabilityServer(grpcSrv, o)
	return o
}
