package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type version struct {
	id          string
	committedAt time.Time
	metadata    []byte
}

type store struct {
	mu           sync.Mutex
	seq          int
	scopeByClaim map[string]string
	byScope      map[string][]version
	releaseFail  string
}

type claimProducer struct {
	genv1.UnimplementedClaimProducerServer
	s *store
}

type dataProcessing struct {
	genv1.UnimplementedDataProcessingServer
	s *store
}

func classed(code codes.Code, class, msg string) error {
	st := status.New(code, msg)
	withInfo, err := st.WithDetails(&errdetails.ErrorInfo{Reason: class, Domain: "content"})
	if err != nil {
		return st.Err()
	}
	return withInfo.Err()
}

func (c claimProducer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed: []genv1.WriteSemantics{genv1.WriteSemantics_WRITE_SEMANTICS_SYNC},
		SupportsSplitScope:    true,
		Protocols:             []string{"claim_producer", "data_processing"},
		DeclaredErrorClasses:  []string{"content/release_refused"},
	}, nil
}

func (c claimProducer) Open(_ context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	scope, _ := json.Marshal(req.GetSelector())
	addr, _ := json.Marshal("content://" + req.GetSelector())
	payload, _ := json.Marshal(map[string]string{"selector": req.GetSelector()})
	c.s.mu.Lock()
	c.s.scopeByClaim[req.GetClaimId()] = req.GetSelector()
	c.s.mu.Unlock()
	return &genv1.OpenResponse{Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
		Address:                addr,
		Payload:                payload,
		ClaimScope:             scope,
		RealizedWriteSemantics: genv1.WriteSemantics_WRITE_SEMANTICS_SYNC,
	}}}, nil
}

func (c claimProducer) Commit(_ context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.s.seq++
	id := fmt.Sprintf("v%d", c.s.seq)
	meta, _ := json.Marshal(map[string]any{"rows": c.s.seq * 10})
	scope := c.s.scopeByClaim[req.GetClaimId()]
	c.s.byScope[scope] = append(c.s.byScope[scope], version{
		id: id, committedAt: time.Now().UTC(), metadata: meta,
	})
	return &genv1.CommitResponse{VersionId: id, ProducerMetadata: meta}, nil
}

func (c claimProducer) Abandon(context.Context, *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	return &genv1.AbandonResponse{}, nil
}

func (c claimProducer) Release(_ context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	c.s.mu.Lock()
	fail := c.s.releaseFail
	c.s.mu.Unlock()
	if fail != "" {
		return nil, classed(codes.FailedPrecondition, fail,
			"content store: Release: the object store refused to drop claim "+req.GetClaimId())
	}
	return &genv1.ReleaseResponse{}, nil
}

func (c claimProducer) SplitScope(context.Context, *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	return &genv1.SplitScopeResponse{}, nil
}

func (c claimProducer) ScopesConflict(_ context.Context, req *genv1.ClaimScopesConflictRequest) (*genv1.ScopesConflictResponse, error) {
	return &genv1.ScopesConflictResponse{Conflicts: string(req.GetClaimScopeA()) == string(req.GetClaimScopeB())}, nil
}

func (d dataProcessing) Capabilities(context.Context, *emptypb.Empty) (*genv1.DataProcessingCapabilities, error) {
	return &genv1.DataProcessingCapabilities{
		DataShapes:       []string{"table"},
		Materializations: []string{"full"},
		PartitionKinds:   []string{"list"},
	}, nil
}

func (d dataProcessing) BeginCandidate(_ context.Context, req *genv1.BeginCandidateRequest) (*genv1.BeginCandidateResponse, error) {
	return &genv1.BeginCandidateResponse{CandidateHandle: []byte(req.GetClaimHandleId())}, nil
}

func (d dataProcessing) CommitCandidate(context.Context, *genv1.CommitCandidateRequest) (*genv1.CommitCandidateResponse, error) {
	return &genv1.CommitCandidateResponse{}, nil
}

func (d dataProcessing) AbandonCandidate(context.Context, *genv1.AbandonCandidateRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (d dataProcessing) ListVersions(_ context.Context, req *genv1.ListVersionsRequest) (*genv1.ListVersionsResponse, error) {
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	out := &genv1.ListVersionsResponse{}
	for _, v := range d.s.byScope[d.s.scopeByClaim[req.GetClaimHandleId()]] {
		out.Versions = append(out.Versions, &genv1.VersionMetadata{
			VersionId:        v.id,
			CommittedAt:      timestamppb.New(v.committedAt),
			ProducerMetadata: v.metadata,
		})
	}
	return out, nil
}

func (d dataProcessing) ListPartitions(context.Context, *genv1.ListPartitionsRequest) (*genv1.ListPartitionsResponse, error) {
	return &genv1.ListPartitionsResponse{}, nil
}

func (d dataProcessing) GetVersionSchema(context.Context, *genv1.GetVersionSchemaRequest) (*genv1.GetVersionSchemaResponse, error) {
	return &genv1.GetVersionSchemaResponse{Schema: []byte(`{"type":"object"}`)}, nil
}

func main() {
	bind := flag.String("bind", "127.0.0.1:9500", "listen address")
	failRelease := flag.String("fail-release", "", "error class the Release verb rejects with")
	flag.Parse()

	s := &store{
		scopeByClaim: map[string]string{},
		byScope:      map[string][]version{},
		releaseFail:  *failRelease,
	}
	lis, err := net.Listen("tcp", *bind)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(srv, claimProducer{s: s})
	genv1.RegisterDataProcessingServer(srv, dataProcessing{s: s})
	log.Printf("content producer listening on %s", *bind)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
