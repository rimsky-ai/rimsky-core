// A third-party claim producer carrying the typed-data mix-in, built the same
// way as the permissive-peer-build experiment's peer: its own Go module whose
// only rimsky requirement is the permissively licensed protocols module.
//
// It serves three protocols on one endpoint:
//
//	ClaimProducer   - including SplitScope, so a node can fan out over it
//	DataProcessing  - the typed-data mix-in the story is about
//	Executor        - so the fan-out children have something to dispatch to
//
// Its own HTTP side lets the run inspect what rimsky asked of it:
//
//	GET /state  -> per-verb call counts, staged candidates, committed versions
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const versionSchema = `{"type":"object","properties":{"rows":{"type":"integer"}}}`

type candidate struct {
	Handle       string
	ClaimHandle  string
	PartitionKey string
	Abandoned    bool
	Committed    bool
}

type version struct {
	VersionID    string
	ClaimHandle  string
	PartitionKey string
	CommittedAt  time.Time
}

type store struct {
	mu         sync.Mutex
	counts     map[string]int
	candidates map[string]*candidate
	byIdem     map[string]string
	versions   []version
	seq        int
	splits     map[string][]string
}

func newStore() *store {
	return &store{
		counts:     map[string]int{},
		candidates: map[string]*candidate{},
		byIdem:     map[string]string{},
		splits:     map[string][]string{},
	}
}

func (s *store) bump(verb string) {
	s.mu.Lock()
	s.counts[verb]++
	s.mu.Unlock()
}

func (s *store) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := map[string]int{}
	for k, v := range s.counts {
		counts[k] = v
	}
	staged := 0
	abandoned := 0
	for _, c := range s.candidates {
		switch {
		case c.Abandoned:
			abandoned++
		case !c.Committed:
			staged++
		}
	}
	versions := make([]map[string]any, 0, len(s.versions))
	for _, v := range s.versions {
		versions = append(versions, map[string]any{
			"version_id":      v.VersionID,
			"claim_handle_id": v.ClaimHandle,
			"partition_key":   v.PartitionKey,
		})
	}
	return map[string]any{
		"counts":               counts,
		"open_candidates":      staged,
		"abandoned_candidates": abandoned,
		"versions":             versions,
		"splits":               s.splits,
	}
}

type claimProducer struct {
	genv1.UnimplementedClaimProducerServer
	s *store
}

func (p claimProducer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	p.s.bump("ClaimProducer.Capabilities")
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed:  []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC},
		SupportsSplitScope:     true,
		SupportsScopesConflict: true,
		Protocols:              []string{"claim_producer", "data_processing"},
		DeclaredErrorClasses:   []string{"typed/unavailable"},
	}, nil
}

func (p claimProducer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	p.s.bump("ClaimProducer.Open")
	scope, _ := json.Marshal(map[string]any{"dataset": req.GetSelector()})
	addr, _ := json.Marshal(map[string]any{"dataset": req.GetSelector()})
	payload, _ := json.Marshal(map[string]any{"dataset": req.GetSelector(), "partition": ""})
	log.Printf("Open claim=%s selector=%s intent=%s", req.GetClaimId(), req.GetSelector(), req.GetIntent())
	return &genv1.OpenResponse{Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
		Address:                addr,
		Payload:                payload,
		ClaimScope:             scope,
		RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC,
	}}}, nil
}

func (p claimProducer) Commit(_ context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	p.s.bump("ClaimProducer.Commit")
	meta, _ := json.Marshal(map[string]any{"committed": true})
	return &genv1.CommitResponse{VersionId: "claim-" + req.GetClaimId(), ProducerMetadata: meta}, nil
}

func (p claimProducer) Abandon(context.Context, *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	p.s.bump("ClaimProducer.Abandon")
	return &genv1.AbandonResponse{}, nil
}

func (p claimProducer) Release(context.Context, *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	p.s.bump("ClaimProducer.Release")
	return &genv1.ReleaseResponse{}, nil
}

func (p claimProducer) SplitScope(_ context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	p.s.bump("ClaimProducer.SplitScope")
	var ask struct {
		Parts int    `json:"parts"`
		Fail  string `json:"fail"`
	}
	if err := json.Unmarshal(req.GetPartitionRequest(), &ask); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "partition_request is not JSON: %v", err)
	}
	if ask.Parts <= 0 {
		return nil, status.Error(codes.InvalidArgument, "partition_request.parts must be > 0")
	}
	keys := make([]string, 0, ask.Parts)
	out := &genv1.SplitScopeResponse{}
	for i := 0; i < ask.Parts; i++ {
		key := fmt.Sprintf("part-%d", i)
		keys = append(keys, key)
		scope, _ := json.Marshal(map[string]any{"partition": key})
		addr, _ := json.Marshal(map[string]any{"partition": key})
		payload, _ := json.Marshal(map[string]any{"partition": key, "fail": ask.Fail == key || ask.Fail == "all"})
		out.SubScopes = append(out.SubScopes, &genv1.SubScopeDescriptor{
			ClaimScopeData: scope,
			PartitionKey:   key,
			Address:        addr,
			Payload:        payload,
			LeaseToken:     "lease-" + key,
		})
	}
	p.s.mu.Lock()
	p.s.splits[req.GetClaimHandleId()] = keys
	p.s.mu.Unlock()
	log.Printf("SplitScope claim=%s parts=%d", req.GetClaimHandleId(), ask.Parts)
	return out, nil
}

func (p claimProducer) ScopesConflict(_ context.Context, req *genv1.ClaimScopesConflictRequest) (*genv1.ScopesConflictResponse, error) {
	p.s.bump("ClaimProducer.ScopesConflict")
	return &genv1.ScopesConflictResponse{Conflicts: string(req.GetClaimScopeA()) == string(req.GetClaimScopeB())}, nil
}

type claimProducerObservability struct {
	genv1.UnimplementedClaimProducerObservabilityServer
	s *store
}

func (o claimProducerObservability) Capabilities(context.Context, *genv1.GetClaimProducerCapabilitiesRequest) (*genv1.ClaimProducerObservabilityCapabilities, error) {
	o.s.bump("ClaimProducerObservability.Capabilities")
	return &genv1.ClaimProducerObservabilityCapabilities{
		SupportsClaimGet:    false,
		SupportsClaimStream: false,
		SupportsListClaims:  false,
	}, nil
}

type dataProcessing struct {
	genv1.UnimplementedDataProcessingServer
	s *store
}

func (d dataProcessing) Capabilities(context.Context, *emptypb.Empty) (*genv1.DataProcessingCapabilities, error) {
	d.s.bump("DataProcessing.Capabilities")
	return &genv1.DataProcessingCapabilities{
		DataShapes:       []string{"table"},
		Materializations: []string{"main"},
		PartitionKinds:   []string{"list"},
		Aggregators:      []string{"union"},
	}, nil
}

func partitionKeyOf(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var probe struct {
		PartitionKey string `json:"partition_key"`
		Partition    string `json:"partition"`
	}
	_ = json.Unmarshal(raw, &probe)
	if probe.PartitionKey != "" {
		return probe.PartitionKey
	}
	return probe.Partition
}

func (d dataProcessing) BeginCandidate(_ context.Context, req *genv1.BeginCandidateRequest) (*genv1.BeginCandidateResponse, error) {
	d.s.bump("DataProcessing.BeginCandidate")
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	if existing, ok := d.s.byIdem[req.GetIdempotencyKey()]; ok && req.GetIdempotencyKey() != "" {
		return &genv1.BeginCandidateResponse{CandidateHandle: []byte(existing)}, nil
	}
	d.s.seq++
	handle := fmt.Sprintf("cand-%d", d.s.seq)
	d.s.candidates[handle] = &candidate{
		Handle:       handle,
		ClaimHandle:  req.GetClaimHandleId(),
		PartitionKey: partitionKeyOf(req.GetSubScopeDescriptor()),
	}
	if req.GetIdempotencyKey() != "" {
		d.s.byIdem[req.GetIdempotencyKey()] = handle
	}
	log.Printf("BeginCandidate claim=%s partition=%q handle=%s",
		req.GetClaimHandleId(), partitionKeyOf(req.GetSubScopeDescriptor()), handle)
	return &genv1.BeginCandidateResponse{CandidateHandle: []byte(handle)}, nil
}

func (d dataProcessing) CommitCandidate(_ context.Context, req *genv1.CommitCandidateRequest) (*genv1.CommitCandidateResponse, error) {
	d.s.bump("DataProcessing.CommitCandidate")
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	c, ok := d.s.candidates[string(req.GetCandidateHandle())]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown candidate_handle %q", string(req.GetCandidateHandle()))
	}
	if c.Abandoned {
		return nil, status.Errorf(codes.FailedPrecondition, "candidate %q was abandoned and garbage-collected", c.Handle)
	}
	if c.Committed {
		return nil, status.Errorf(codes.FailedPrecondition, "candidate %q was already committed", c.Handle)
	}
	c.Committed = true
	d.s.seq++
	v := version{
		VersionID:    fmt.Sprintf("v-%d", d.s.seq),
		ClaimHandle:  c.ClaimHandle,
		PartitionKey: c.PartitionKey,
		CommittedAt:  time.Now().UTC(),
	}
	d.s.versions = append(d.s.versions, v)
	meta, _ := json.Marshal(map[string]any{"partition_key": c.PartitionKey, "rows": 1})
	log.Printf("CommitCandidate handle=%s version=%s claim=%s partition=%q", c.Handle, v.VersionID, c.ClaimHandle, c.PartitionKey)
	return &genv1.CommitCandidateResponse{CandidateMetadata: meta}, nil
}

func (d dataProcessing) AbandonCandidate(_ context.Context, req *genv1.AbandonCandidateRequest) (*emptypb.Empty, error) {
	d.s.bump("DataProcessing.AbandonCandidate")
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	c, ok := d.s.candidates[string(req.GetCandidateHandle())]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown candidate_handle %q", string(req.GetCandidateHandle()))
	}
	c.Abandoned = true
	log.Printf("AbandonCandidate handle=%s claim=%s partition=%q", c.Handle, c.ClaimHandle, c.PartitionKey)
	return &emptypb.Empty{}, nil
}

func (d dataProcessing) ListVersions(_ context.Context, req *genv1.ListVersionsRequest) (*genv1.ListVersionsResponse, error) {
	d.s.bump("DataProcessing.ListVersions")
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	out := &genv1.ListVersionsResponse{}
	for _, v := range d.s.versions {
		if v.ClaimHandle != req.GetClaimHandleId() {
			continue
		}
		meta, _ := json.Marshal(map[string]any{"partition_key": v.PartitionKey})
		out.Versions = append(out.Versions, &genv1.VersionMetadata{
			VersionId:        v.VersionID,
			CommittedAt:      timestamppb.New(v.CommittedAt),
			ProducerMetadata: meta,
		})
	}
	return out, nil
}

func (d dataProcessing) ListPartitions(_ context.Context, req *genv1.ListPartitionsRequest) (*genv1.ListPartitionsResponse, error) {
	d.s.bump("DataProcessing.ListPartitions")
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	seen := map[string]struct{}{}
	for _, v := range d.s.versions {
		if v.ClaimHandle != req.GetClaimHandleId() {
			continue
		}
		if req.GetVersionId() != "" && v.VersionID != req.GetVersionId() {
			continue
		}
		seen[v.PartitionKey] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := &genv1.ListPartitionsResponse{}
	for _, k := range keys {
		meta, _ := json.Marshal(map[string]any{"partition_key": k})
		out.Partitions = append(out.Partitions, &genv1.PartitionDescriptor{
			PartitionKey:      k,
			PartitionMetadata: meta,
		})
	}
	return out, nil
}

func (d dataProcessing) GetVersionSchema(_ context.Context, req *genv1.GetVersionSchemaRequest) (*genv1.GetVersionSchemaResponse, error) {
	d.s.bump("DataProcessing.GetVersionSchema")
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	for _, v := range d.s.versions {
		if v.ClaimHandle == req.GetClaimHandleId() && (req.GetVersionId() == "" || v.VersionID == req.GetVersionId()) {
			return &genv1.GetVersionSchemaResponse{Schema: []byte(versionSchema)}, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "no version %q for claim %q", req.GetVersionId(), req.GetClaimHandleId())
}

func envPort(key string, dflt int) int {
	v := os.Getenv(key)
	if v == "" {
		return dflt
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("typed-data-peer: %s=%q is not a port number", key, v)
	}
	return n
}

func main() {
	label := os.Getenv("PEER_LABEL")
	if label == "" {
		label = "typed-data-peer"
	}
	ctx := context.Background()
	s := newStore()

	srv, identity, err := peerauth.NewGRPCServer(ctx, label)
	if err != nil {
		log.Fatalf("typed-data-peer: peer-auth setup failed: %v", err)
	}
	identity.StartMaintain(ctx, label)
	genv1.RegisterClaimProducerServer(srv, claimProducer{s: s})
	genv1.RegisterClaimProducerObservabilityServer(srv, claimProducerObservability{s: s})
	genv1.RegisterDataProcessingServer(srv, dataProcessing{s: s})
	genv1.RegisterExecutorServer(srv, executor{label: label})
	genv1.RegisterExecutorObservabilityServer(srv, observability{})

	mux := http.NewServeMux()
	mux.HandleFunc("/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(s.snapshot())
	})
	go func() {
		if err := http.ListenAndServe(fmt.Sprintf(":%d", envPort("PEER_HTTP_PORT", 9701)), mux); err != nil {
			log.Fatalf("typed-data-peer: http: %v", err)
		}
	}()

	addr := fmt.Sprintf("0.0.0.0:%d", envPort("PEER_PORT", 9700))
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("typed-data-peer: listen %s: %v", addr, err)
	}
	log.Printf("typed-data-peer listening on %s (peer_auth_mtls=%v)", addr, identity.Enabled())
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("typed-data-peer: serve: %v", err)
	}
}
