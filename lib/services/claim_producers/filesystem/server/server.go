// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	claimproducer "github.com/rimsky-ai/rimsky-core/lib/protocols/claimproducer"
	bridge "github.com/rimsky-ai/rimsky-core/lib/protocols/serverkit"
	fsstore "github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/filesystem/store"
	"github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/shared/lifecycle"
	"github.com/rimsky-ai/rimsky-core/lib/services/claim_producers/shared/listarray"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/peerauth"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const gracefulStopBudget = 5 * time.Second

type Config struct {
	Root             string
	PickPolicies     map[string]*fsstore.PickPolicy
	SweepInterval    time.Duration
	HTTPBridgeURL    string
	EnableLifecycle  bool
	LedgerMaxRecords int
}

func New(cfg Config) (*Server, error) {
	st, err := fsstore.New(fsstore.Config{
		Root:             cfg.Root,
		PickPolicies:     cfg.PickPolicies,
		LedgerMaxRecords: cfg.LedgerMaxRecords,
	})
	if err != nil {
		return nil, err
	}
	return &Server{store: st, protocols: declaredProtocols(cfg)}, nil
}

func declaredProtocols(cfg Config) []string {
	var protocols []string
	if cfg.EnableLifecycle {
		protocols = append(protocols, claimproducer.ProtocolLifecycleSubscriber)
	}
	return protocols
}

func (s *Server) RunSweep(ctx context.Context, interval time.Duration) {
	s.store.RunSweep(ctx, interval)
}

func Run(ctx context.Context, cfg Config, grpcLis, httpLis, adminLis net.Listener) error {
	srv, err := New(cfg)
	if err != nil {
		return err
	}

	identity, err := peerauth.LoadFromEnv(ctx, "claim-producer-filesystem")
	if err != nil {
		return err
	}
	identity.StartMaintain(ctx, "claim-producer-filesystem")

	grpcSrv := grpc.NewServer(identity.GRPCServerOptions()...)
	genv1.RegisterClaimProducerServer(grpcSrv, srv)
	var lifecycleSrv *lifecycle.Server
	if cfg.EnableLifecycle {
		lifecycleSrv = lifecycle.NewServer()
		genv1.RegisterLifecycleSubscriberServer(grpcSrv, lifecycleSrv)
	}
	obsSrv := srv.RegisterObservability(grpcSrv, cfg.Root, cfg.PickPolicies)
	obsSrv.SetHTTPBridgeURL(cfg.HTTPBridgeURL)
	go func() {
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Warn("filesystem store: grpc serve", "error", err.Error())
		}
	}()

	mux := http.NewServeMux()
	bridge.Mount(mux, srv)
	if cfg.EnableLifecycle {
		bridge.MountLifecycle(mux, lifecycleSrv)
	}
	bridge.MountObservability(mux, obsSrv)
	httpSrv := identity.HTTPServer(mux)
	go func() {
		if err := identity.RunHTTP(httpSrv, httpLis); err != nil && err != http.ErrServerClosed {
			slog.Warn("filesystem store: http serve", "error", err.Error())
		}
	}()

	var adminSrv *http.Server
	if adminLis != nil {
		adminSrv = &http.Server{Handler: srv.store.AdminHandler()}
		go func() {
			if err := adminSrv.Serve(adminLis); err != nil && err != http.ErrServerClosed {
				slog.Warn("filesystem store: admin serve", "error", err.Error())
			}
		}()
	}

	go srv.RunSweep(ctx, cfg.SweepInterval)

	<-ctx.Done()
	bridge.GracefulStop(grpcSrv, gracefulStopBudget)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), gracefulStopBudget)
	defer cancelShutdown()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("filesystem store: http graceful shutdown", "error", err.Error())
		_ = httpSrv.Close()
	}
	if adminSrv != nil {
		if err := adminSrv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("filesystem store: admin graceful shutdown", "error", err.Error())
			_ = adminSrv.Close()
		}
	}
	return nil
}

type Server struct {
	genv1.UnimplementedClaimProducerServer
	store     *fsstore.Store
	protocols []string
}

func (s *Server) Capabilities(_ context.Context, _ *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	c := s.store.Capabilities()
	out := make([]genv1.WriteSemantics, 0, len(c.WriteSemanticsAllowed))
	for _, ws := range c.WriteSemanticsAllowed {
		out = append(out, bridge.WriteSemanticsToProto(string(ws)))
	}
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: out,
		DeclaredErrorClasses:  c.DeclaredErrorClasses,
		SupportsSplitScope:    c.SupportsSplitScope,
		Protocols:             s.protocols,
	}, nil
}

func (s *Server) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	if intent := req.GetIntent(); intent != "r" && intent != "rw" {
		return nil, status.Errorf(codes.InvalidArgument, "filesystem.Open: intent must be \"r\" or \"rw\", got %q", intent)
	}
	outcome, err := s.store.Open(ctx, req.GetClaimId(), req.GetSelector())
	if err != nil {
		return nil, classedStatus(err)
	}
	if !outcome.Available {
		return &genv1.OpenResponse{
			Result: &genv1.OpenResponse_Unavailable{Unavailable: &genv1.Unavailable{}},
		}, nil
	}
	return &genv1.OpenResponse{
		Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
			Address:                outcome.Result.Address,
			Payload:                outcome.Result.Payload,
			ClaimScope:             outcome.Result.ClaimScope,
			RealizedWriteSemantics: bridge.WriteSemanticsToProto(string(outcome.Result.RealizedWriteSemantics)),
		}},
	}, nil
}

func (s *Server) Commit(ctx context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	if err := s.store.Commit(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress(), req.GetLeaseToken()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.CommitResponse{}, nil
}

func (s *Server) Abandon(ctx context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := s.store.Abandon(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress(), req.GetLeaseToken()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.AbandonResponse{}, nil
}

func (s *Server) Release(ctx context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := s.store.Release(ctx, req.GetClaimId(), req.GetClaimScope(), req.GetAddress(), req.GetLeaseToken()); err != nil {
		return nil, classedStatus(err)
	}
	return &genv1.ReleaseResponse{}, nil
}

type parentClaimInfo struct {
	ClaimID        string
	AbsPath        string
	PickPolicyName string
	PickPolicy     *fsstore.PickPolicy
	HasPickPolicy  bool
}

// @concept: fan-out
func (s *Server) SplitScope(ctx context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	claimID := req.GetClaimHandleId()
	if claimID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "filesystem store: SplitScope: claim_handle_id is required")
	}
	parentPath, ok := s.store.LookupClaimPath(claimID)
	if !ok {
		if rec, found := s.store.Ledger().Get(claimID); found {
			return nil, status.Errorf(codes.FailedPrecondition,
				"filesystem store: SplitScope: claim %q is not OPEN (state=%s)", claimID, rec.State)
		}
		return nil, status.Errorf(codes.NotFound, "filesystem store: SplitScope: unknown claim_handle_id %q", claimID)
	}
	parent := parentClaimInfo{ClaimID: claimID, AbsPath: parentPath}
	if sel, pp, ok := s.store.LookupClaimPickPolicy(claimID); ok {
		parent.PickPolicyName = sel
		parent.PickPolicy = pp
		parent.HasPickPolicy = true
	}

	partitionRequest := req.GetPartitionRequest()
	if len(partitionRequest) == 0 {
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope: partition_request must specify exactly one of list / batch_pick / expand_folder")
	}
	var probe struct {
		List         json.RawMessage `json:"list"`
		BatchPick    json.RawMessage `json:"batch_pick"`
		ExpandFolder json.RawMessage `json:"expand_folder"`
	}
	probeDec := json.NewDecoder(bytes.NewReader(partitionRequest))
	probeDec.DisallowUnknownFields()
	if err := probeDec.Decode(&probe); err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope: partition_request is not a JSON object or contains unknown top-level keys (only list / batch_pick / expand_folder are permitted): %v", err)
	}
	discriminatorCount := 0
	if probe.List != nil {
		discriminatorCount++
	}
	if probe.BatchPick != nil {
		discriminatorCount++
	}
	if probe.ExpandFolder != nil {
		discriminatorCount++
	}
	if discriminatorCount > 1 {
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope: partition_request must specify exactly one of list / batch_pick / expand_folder; got %d discriminators set", discriminatorCount)
	}

	var (
		descs []*genv1.SubScopeDescriptor
		err   error
	)
	switch {
	case probe.List != nil:
		descs, err = s.splitListArray(ctx, parent, partitionRequest)
	case probe.BatchPick != nil:
		descs, err = s.splitBatchPick(ctx, parent, probe.BatchPick)
	case probe.ExpandFolder != nil:
		descs, err = s.splitExpandFolder(ctx, parent, probe.ExpandFolder)
	default:
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope: partition_request must specify exactly one of list / batch_pick / expand_folder")
	}
	if err != nil {
		return nil, err
	}
	return &genv1.SplitScopeResponse{SubScopes: descs}, nil
}

func (s *Server) splitListArray(_ context.Context, parent parentClaimInfo, partitionRequest []byte) ([]*genv1.SubScopeDescriptor, error) {
	req, err := listarray.Unmarshal(partitionRequest)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope list: %v", err)
	}
	for _, el := range req.List {
		if err := validateListElementKey(el.Key); err != nil {
			return nil, status.Errorf(codes.InvalidArgument,
				"filesystem store: SplitScope list: %v", err)
		}
	}
	subs, err := listarray.ToSubScopes(req, func(key string) ([]byte, error) {
		syntheticPath := filepath.Join(parent.AbsPath, "_list", key)
		return s.store.ScopeBytesForAbs(syntheticPath)
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"filesystem store: SplitScope list: synthesize claim_scope: %v", err)
	}
	out := make([]*genv1.SubScopeDescriptor, 0, len(subs))
	for _, sub := range subs {
		out = append(out, &genv1.SubScopeDescriptor{
			PartitionKey:   sub.PartitionKey,
			ClaimScopeData: sub.ClaimScopeData,
			Payload:        sub.Payload,
			Address:        sub.Address,
		})
	}
	return out, nil
}

func validateListElementKey(key string) error {
	if strings.ContainsAny(key, "/\\") {
		return fmt.Errorf("element key %q must not contain a path separator", key)
	}
	if key == "." || key == ".." {
		return fmt.Errorf("element key %q is not a valid list element key", key)
	}
	return nil
}

func (s *Server) splitBatchPick(ctx context.Context, parent parentClaimInfo, batchPickJSON []byte) ([]*genv1.SubScopeDescriptor, error) {
	var body struct {
		MaxItems int    `json:"max_items"`
		Policy   string `json:"policy"`
	}
	dec := json.NewDecoder(bytes.NewReader(batchPickJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope batch_pick: invalid body or unknown keys (only max_items / policy are permitted): %v", err)
	}
	if body.MaxItems <= 0 {
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope batch_pick: max_items must be > 0, got %d", body.MaxItems)
	}
	policyName := parent.PickPolicyName
	if body.Policy != "" {
		if _, _, ok := s.store.LookupPickPolicy(body.Policy); !ok {
			return nil, status.Errorf(codes.InvalidArgument,
				"filesystem store: SplitScope batch_pick: policy %q is not declared in the store's pick_policies",
				body.Policy)
		}
		policyName = body.Policy
	} else if !parent.HasPickPolicy {
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope batch_pick: parent claim %q was not opened against a pick policy and no `policy` was specified in batch_pick; "+
				"batch_pick pops from the parent claim's pick policy by default — supply `policy` to override or open the parent against a queue-backed selector",
			parent.ClaimID)
	}
	claimIDs := make([]string, body.MaxItems)
	for i := range claimIDs {
		claimIDs[i] = uuid.New().String()
	}
	items, err := s.store.BatchPop(ctx, policyName, claimIDs)
	if err != nil {
		return nil, classedStatus(err)
	}
	out := make([]*genv1.SubScopeDescriptor, 0, len(items))
	for _, item := range items {
		out = append(out, &genv1.SubScopeDescriptor{
			PartitionKey:   item.Folder,
			ClaimScopeData: item.ClaimScopeBytes,
			Address:        item.AddressBytes,
			Payload:        item.PayloadBytes,
			LeaseToken:     item.LeaseToken,
		})
	}
	return out, nil
}

func (s *Server) splitExpandFolder(_ context.Context, parent parentClaimInfo, expandFolderJSON []byte) ([]*genv1.SubScopeDescriptor, error) {
	body := struct {
		Filter string `json:"filter"`
		Depth  int    `json:"depth"`
		Kind   string `json:"kind"`
	}{Filter: "*", Depth: 1, Kind: "files"}
	if len(expandFolderJSON) > 0 && string(expandFolderJSON) != "null" {
		dec := json.NewDecoder(bytes.NewReader(expandFolderJSON))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			return nil, status.Errorf(codes.InvalidArgument,
				"filesystem store: SplitScope expand_folder: invalid body or unknown keys (only filter / depth / kind are permitted): %v", err)
		}
	}
	if body.Filter == "" {
		body.Filter = "*"
	}
	if body.Depth <= 0 {
		body.Depth = 1
	}
	if body.Kind == "" {
		body.Kind = "files"
	}
	switch body.Kind {
	case "files", "folders", "both":
	default:
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope expand_folder: kind must be one of files|folders|both, got %q", body.Kind)
	}
	if body.Kind == "both" && body.Depth > 1 {
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope expand_folder: kind=both with depth>1 (got %d) would emit overlapping sub-claims "+
				"(a directory and its descendants are not disjoint partitions); use kind=files or kind=folders, or set depth=1",
			body.Depth)
	}
	if body.Kind == "folders" && body.Depth > 1 {
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope expand_folder: kind=folders with depth>1 (got %d) would emit overlapping sub-claims "+
				"(a folder and its descendant folders are not disjoint partitions); set depth=1",
			body.Depth)
	}
	if _, err := filepath.Match(body.Filter, "probe"); err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"filesystem store: SplitScope expand_folder: invalid filter glob %q: %v", body.Filter, err)
	}

	storeRoot := s.store.Root()
	rel, relErr := filepath.Rel(storeRoot, parent.AbsPath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, status.Errorf(codes.FailedPrecondition,
			"filesystem store: SplitScope expand_folder: parent path %q is not contained in store root %q", parent.AbsPath, storeRoot)
	}
	parentInfo, err := os.Stat(parent.AbsPath)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"filesystem store: SplitScope expand_folder: cannot stat parent path %q: %v", parent.AbsPath, err)
	}
	if !parentInfo.IsDir() {
		return nil, status.Errorf(codes.FailedPrecondition,
			"filesystem store: SplitScope expand_folder: parent path %q is not a directory", parent.AbsPath)
	}

	out := make([]*genv1.SubScopeDescriptor, 0)
	walkErr := filepath.WalkDir(parent.AbsPath, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if p == parent.AbsPath {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		relPath, relErr := filepath.Rel(parent.AbsPath, p)
		if relErr != nil {
			return relErr
		}
		relDepth := len(strings.Split(filepath.ToSlash(relPath), "/"))
		if relDepth > body.Depth {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		matched, mErr := filepath.Match(body.Filter, d.Name())
		if mErr != nil {
			return mErr
		}
		if !matched {
			return nil
		}
		isFile := !d.IsDir()
		isFolder := d.IsDir()
		switch body.Kind {
		case "files":
			if !isFile {
				return nil
			}
		case "folders":
			if !isFolder {
				return nil
			}
		}
		addrBytes, mErr := json.Marshal(p)
		if mErr != nil {
			return mErr
		}
		scopeBytes, sErr := s.store.ScopeBytesForAbs(p)
		if sErr != nil {
			return sErr
		}
		out = append(out, &genv1.SubScopeDescriptor{
			PartitionKey:   filepath.ToSlash(relPath),
			ClaimScopeData: scopeBytes,
			Address:        addrBytes,
			Payload:        nil,
		})
		return nil
	})
	if walkErr != nil {
		return nil, status.Errorf(codes.Internal,
			"filesystem store: SplitScope expand_folder: walk %q: %v", parent.AbsPath, walkErr)
	}
	return out, nil
}

func classedStatus(err error) error {
	if err == nil {
		return nil
	}
	var ve *fsstore.ValidationError
	if errors.As(err, &ve) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	var ce *fsstore.ClassedError
	if !errors.As(err, &ce) || ce.Class == "" {
		return status.Error(codes.Internal, err.Error())
	}
	st := status.New(codes.Internal, ce.Error())
	withInfo, derr := st.WithDetails(&errdetails.ErrorInfo{
		Reason: ce.Class,
		Domain: "rimsky.claim-producer-filesystem",
	})
	if derr != nil {
		return st.Err()
	}
	return withInfo.Err()
}
