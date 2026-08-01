// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	fsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/filesystem/store"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type ObservabilityServer struct {
	genv1.UnimplementedClaimProducerObservabilityServer
	store             *fsstore.Store
	pickPolicies      map[string]*fsstore.PickPolicy
	root              string
	httpBridgeURLOnce sync.Once
	httpBridgeURL     string
	idleTimeout       time.Duration
}

func NewObservabilityServer(store *fsstore.Store, root string, pickPolicies map[string]*fsstore.PickPolicy) *ObservabilityServer {
	return &ObservabilityServer{store: store, root: root, pickPolicies: pickPolicies, idleTimeout: defaultObsIdleTimeout}
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
			{
				Name:        "pick_policies",
				Title:       "Pick policies",
				Description: "Configured pick policies and current queue depths",
			},
			{
				Name:        "policy_items",
				Title:       "Items in a policy",
				Description: "Items currently available or in-progress for one selector",
				Params: []*genv1.AdminViewParam{
					{Name: "selector", Type: "string", Required: true},
				},
			},
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
	if rec.State != fsstore.ClaimStateOpen {
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

func claimRecordToDetail(rec *fsstore.ClaimRecord) *genv1.ClaimDetail {
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

func claimEventToProto(ev fsstore.ClaimEvent) *genv1.ClaimEvent {
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

func claimStateToProto(st fsstore.ClaimState) genv1.ClaimState {
	switch st {
	case fsstore.ClaimStateOpen:
		return genv1.ClaimState_OPEN
	case fsstore.ClaimStateCommitted:
		return genv1.ClaimState_COMMITTED
	case fsstore.ClaimStateAbandoned:
		return genv1.ClaimState_ABANDONED
	case fsstore.ClaimStateReleased:
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

func (s *ObservabilityServer) GetAdminView(_ context.Context, req *genv1.GetAdminViewRequest) (*genv1.AdminView, error) {
	switch req.GetViewName() {
	case "pick_policies":
		return s.pickPoliciesView()
	case "policy_items":
		params := req.GetParams().AsMap()
		selector, _ := params["selector"].(string)
		if selector == "" {
			return nil, status.Error(codes.InvalidArgument, "selector param required")
		}
		return s.policyItemsView(selector)
	default:
		return nil, status.Errorf(codes.NotFound, "unknown admin view %q", req.GetViewName())
	}
}

func (s *ObservabilityServer) pickPoliciesView() (*genv1.AdminView, error) {
	rows := make([]any, 0, len(s.pickPolicies))
	selectors := make([]string, 0, len(s.pickPolicies))
	for sel := range s.pickPolicies {
		selectors = append(selectors, sel)
	}
	sort.Strings(selectors)
	for _, sel := range selectors {
		pp := s.pickPolicies[sel]
		dirRoot := fsstore.PolicyStateDir(s.root, sel)
		avail, _ := countDir(filepath.Join(dirRoot, "available"))
		inProg, _ := countDir(filepath.Join(dirRoot, "in_progress"))
		rows = append(rows, map[string]any{
			"selector":                   sel,
			"root":                       pp.Root,
			"available_count":            avail,
			"in_progress_count":          inProg,
			"visibility_timeout_seconds": int(pp.VisibilityTimeout.Seconds()),
			"sync_strategy":              pp.SyncStrategy,
			"on_commit":                  string(pp.OnCommit.Kind),
			"on_commit_move_target":      pp.OnCommit.MoveTarget,
			"on_give_up":                 string(pp.OnGiveUp.Kind),
			"on_give_up_move_target":     pp.OnGiveUp.MoveTarget,
		})
	}
	data, _ := structpb.NewStruct(map[string]any{"rows": rows})
	return &genv1.AdminView{
		Schema: &genv1.AdminViewSchema{Columns: []*genv1.AdminViewColumn{
			{Name: "selector", Type: "string"},
			{Name: "root", Type: "string"},
			{Name: "available_count", Type: "int"},
			{Name: "in_progress_count", Type: "int"},
			{Name: "visibility_timeout_seconds", Type: "int"},
			{Name: "sync_strategy", Type: "string"},
			{Name: "on_commit", Type: "string"},
			{Name: "on_commit_move_target", Type: "string"},
			{Name: "on_give_up", Type: "string"},
			{Name: "on_give_up_move_target", Type: "string"},
		}},
		Data:       data,
		RenderHint: "table",
	}, nil
}

func (s *ObservabilityServer) policyItemsView(selector string) (*genv1.AdminView, error) {
	if _, ok := s.pickPolicies[selector]; !ok {
		return nil, status.Errorf(codes.NotFound, "selector %q not configured", selector)
	}
	dirRoot := fsstore.PolicyStateDir(s.root, selector)
	rows := make([]any, 0)
	for _, state := range []string{"available", "in_progress"} {
		entries, err := os.ReadDir(filepath.Join(dirRoot, state))
		if err != nil {
			continue
		}
		for _, e := range entries {
			rows = append(rows, map[string]any{
				"folder": e.Name(),
				"state":  state,
			})
		}
	}
	data, _ := structpb.NewStruct(map[string]any{"rows": rows})
	return &genv1.AdminView{
		Schema: &genv1.AdminViewSchema{Columns: []*genv1.AdminViewColumn{
			{Name: "folder", Type: "string"},
			{Name: "state", Type: "string"},
		}},
		Data:       data,
		RenderHint: "table",
	}, nil
}

func countDir(path string) (int, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

func (s *Server) RegisterObservability(grpcSrv *grpc.Server, root string, pickPolicies map[string]*fsstore.PickPolicy) *ObservabilityServer {
	o := NewObservabilityServer(s.store, root, pickPolicies)
	genv1.RegisterClaimProducerObservabilityServer(grpcSrv, o)
	return o
}
