package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type call struct {
	Verb       string          `json:"verb"`
	ClaimID    string          `json:"claim_id"`
	Selector   string          `json:"selector,omitempty"`
	Intent     string          `json:"intent,omitempty"`
	Alias      string          `json:"alias,omitempty"`
	Lifetime   string          `json:"lifetime,omitempty"`
	InstanceID string          `json:"instance_id,omitempty"`
	RunScopeID string          `json:"run_scope_id,omitempty"`
	Data       string          `json:"data,omitempty"`
	Scope      string          `json:"claim_scope,omitempty"`
	Address    string          `json:"address,omitempty"`
	Result     string          `json:"result,omitempty"`
	Extra      json.RawMessage `json:"extra,omitempty"`
	At         string          `json:"at"`
}

type openClaim struct {
	selector string
	intent   string
	scope    []byte
}

type producer struct {
	genv1.UnimplementedClaimProducerServer

	name             string
	semantics        genv1.WriteSemantics
	payload          []byte
	versionID        string
	producerMetadata []byte
	prefixConflict   bool
	splitScope       bool
	nonIdempotent    bool
	serializeReaders bool
	unavailableClass string

	mu        sync.Mutex
	log       []call
	open      map[string]openClaim
	closed    map[string]bool
	writeOpen map[string]int
}

func (p *producer) record(c call) {
	c.At = time.Now().UTC().Format(time.RFC3339Nano)
	p.mu.Lock()
	p.log = append(p.log, c)
	p.mu.Unlock()
}

func (p *producer) Capabilities(context.Context, *genv1.CapabilitiesRequest) (*genv1.CapabilitiesResponse, error) {
	p.record(call{Verb: "Capabilities"})
	return &genv1.CapabilitiesResponse{
		WriteSemanticsAllowed:  []genv1.WriteSemantics{p.semantics},
		SupportsSplitScope:     p.splitScope,
		SupportsScopesConflict: p.prefixConflict,
		DeclaredErrorClasses:   []string{p.name + "/exhausted"},
	}, nil
}

func scopeBytes(selector string) []byte {
	b, _ := json.Marshal(map[string]string{"selector": selector})
	return b
}

func addressBytes(name, selector string) []byte {
	b, _ := json.Marshal(fmt.Sprintf("%s://%s", name, strings.TrimPrefix(selector, "/")))
	return b
}

func (p *producer) Open(ctx context.Context, req *genv1.OpenRequest) (*genv1.OpenResponse, error) {
	sel := req.GetSelector()
	scope := scopeBytes(sel)
	addr := addressBytes(p.name, sel)

	if p.unavailableClass != "" {
		p.record(call{Verb: "Open", ClaimID: req.GetClaimId(), Selector: sel, Intent: req.GetIntent(),
			Alias: req.GetAlias(), Lifetime: req.GetLifetime(), InstanceID: req.GetInstanceId(),
			RunScopeID: req.GetRunScopeId(), Data: string(req.GetData()), Result: "unavailable"})
		return &genv1.OpenResponse{Result: &genv1.OpenResponse_Unavailable{
			Unavailable: &genv1.Unavailable{ErrorClass: p.unavailableClass}}}, nil
	}

	if p.serializeReaders {
		if req.GetIntent() == "rw" || req.GetIntent() == "w" {
			p.mu.Lock()
			p.writeOpen[sel]++
			p.mu.Unlock()
		} else {
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				p.mu.Lock()
				busy := p.writeOpen[sel] > 0
				p.mu.Unlock()
				if !busy {
					break
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
			}
		}
	}

	p.mu.Lock()
	p.open[req.GetClaimId()] = openClaim{selector: sel, intent: req.GetIntent(), scope: scope}
	p.mu.Unlock()

	p.record(call{Verb: "Open", ClaimID: req.GetClaimId(), Selector: sel, Intent: req.GetIntent(),
		Alias: req.GetAlias(), Lifetime: req.GetLifetime(), InstanceID: req.GetInstanceId(),
		RunScopeID: req.GetRunScopeId(), Data: string(req.GetData()),
		Scope: string(scope), Address: string(addr), Result: "acquired"})

	return &genv1.OpenResponse{Result: &genv1.OpenResponse_Acquired{Acquired: &genv1.Acquired{
		Address:                addr,
		Payload:                p.payload,
		ClaimScope:             scope,
		RealizedWriteSemantics: p.semantics,
	}}}, nil
}

func (p *producer) closeClaim(verb string, claimID string, scope, addr []byte) error {
	p.mu.Lock()
	oc, wasOpen := p.open[claimID]
	alreadyClosed := p.closed[claimID]
	if wasOpen {
		delete(p.open, claimID)
		if p.serializeReaders && (oc.intent == "rw" || oc.intent == "w") && p.writeOpen[oc.selector] > 0 {
			p.writeOpen[oc.selector]--
		}
	}
	p.closed[claimID] = true
	p.mu.Unlock()

	p.record(call{Verb: verb, ClaimID: claimID, Selector: oc.selector, Scope: string(scope), Address: string(addr)})

	if p.nonIdempotent && alreadyClosed {
		return status.Errorf(codes.FailedPrecondition, "%s: claim %s already terminal", verb, claimID)
	}
	return nil
}

func (p *producer) Commit(_ context.Context, req *genv1.CommitRequest) (*genv1.CommitResponse, error) {
	if err := p.closeClaim("Commit", req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.CommitResponse{VersionId: p.versionID, ProducerMetadata: p.producerMetadata}, nil
}

func (p *producer) Abandon(_ context.Context, req *genv1.AbandonRequest) (*genv1.AbandonResponse, error) {
	if err := p.closeClaim("Abandon", req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.AbandonResponse{}, nil
}

func (p *producer) Release(_ context.Context, req *genv1.ReleaseRequest) (*genv1.ReleaseResponse, error) {
	if err := p.closeClaim("Release", req.GetClaimId(), req.GetClaimScope(), req.GetAddress()); err != nil {
		return nil, err
	}
	return &genv1.ReleaseResponse{}, nil
}

type listPartition struct {
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type partitionRequest struct {
	List []listPartition `json:"list"`
}

func (p *producer) SplitScope(_ context.Context, req *genv1.SplitScopeRequest) (*genv1.SplitScopeResponse, error) {
	if !p.splitScope {
		return nil, status.Error(codes.Unimplemented, "split_scope unsupported")
	}
	p.mu.Lock()
	oc, ok := p.open[req.GetClaimHandleId()]
	p.mu.Unlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound, "SplitScope: unknown claim_handle_id %q", req.GetClaimHandleId())
	}
	var pr partitionRequest
	if err := json.Unmarshal(req.GetPartitionRequest(), &pr); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "SplitScope: %v", err)
	}
	out := make([]*genv1.SubScopeDescriptor, 0, len(pr.List))
	for _, el := range pr.List {
		out = append(out, &genv1.SubScopeDescriptor{
			ClaimScopeData: scopeBytes(strings.TrimSuffix(oc.selector, "/") + "/" + el.Key),
			PartitionKey:   el.Key,
			Payload:        []byte(el.Payload),
		})
	}
	p.record(call{Verb: "SplitScope", ClaimID: req.GetClaimHandleId(), Selector: oc.selector,
		Result: fmt.Sprintf("%d sub-scopes", len(out))})
	return &genv1.SplitScopeResponse{SubScopes: out}, nil
}

func selectorOf(scope []byte) string {
	var m map[string]string
	if err := json.Unmarshal(scope, &m); err == nil {
		if sel, present := m["selector"]; present {
			return sel
		}
	}
	var raw string
	if err := json.Unmarshal(scope, &raw); err == nil {
		return raw
	}
	return string(scope)
}

func lastSegment(s string) string {
	trimmed := strings.TrimSuffix(s, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

func prefixOverlap(a, b string) bool {
	if a == b {
		return true
	}
	return lastSegment(a) == lastSegment(b)
}

func (p *producer) ScopesConflict(_ context.Context, req *genv1.ClaimScopesConflictRequest) (*genv1.ScopesConflictResponse, error) {
	if !p.prefixConflict {
		return nil, status.Error(codes.Unimplemented, "scopes_conflict unsupported")
	}
	a, b := selectorOf(req.GetClaimScopeA()), selectorOf(req.GetClaimScopeB())
	conflicts := prefixOverlap(a, b)
	p.record(call{Verb: "ScopesConflict", Selector: a + " ~ " + b, Result: fmt.Sprintf("%v", conflicts)})
	return &genv1.ScopesConflictResponse{Conflicts: conflicts}, nil
}

func parseSemantics(s string) genv1.WriteSemantics {
	switch s {
	case "sync":
		return genv1.WriteSemantics_WRITE_SEMANTICS_SYNC
	case "staged_async":
		return genv1.WriteSemantics_WRITE_SEMANTICS_STAGED_ASYNC
	case "blocking_async":
		return genv1.WriteSemantics_WRITE_SEMANTICS_BLOCKING_ASYNC
	case "read_only":
		return genv1.WriteSemantics_WRITE_SEMANTICS_READ_ONLY
	}
	log.Fatalf("unknown write semantics %q", s)
	return genv1.WriteSemantics_WRITE_SEMANTICS_UNKNOWN
}

func main() {
	grpcAddr := flag.String("grpc", "127.0.0.1:9401", "gRPC listen address")
	httpAddr := flag.String("http", "", "introspection HTTP listen address")
	name := flag.String("name", "custom", "producer name used in synthesized addresses")
	semantics := flag.String("semantics", "sync", "advertised write semantics")
	payload := flag.String("payload", "", "JSON payload returned on Open")
	versionID := flag.String("version-id", "", "version_id returned on Commit")
	producerMeta := flag.String("producer-metadata", "", "producer_metadata returned on Commit")
	prefixConflict := flag.Bool("scopes-conflict", false, "advertise ScopesConflict with prefix-containment overlap")
	splitScope := flag.Bool("split-scope", false, "advertise SplitScope with list partitioning")
	nonIdempotent := flag.Bool("non-idempotent-terminals", false, "reject a retried terminal verb")
	serializeReaders := flag.Bool("serialize-readers", false, "block a read Open while a write claim is open on the same selector")
	unavailable := flag.String("unavailable-class", "", "always answer Open with Unavailable carrying this error class")
	flag.Parse()

	p := &producer{
		name:             *name,
		semantics:        parseSemantics(*semantics),
		payload:          []byte(*payload),
		versionID:        *versionID,
		producerMetadata: []byte(*producerMeta),
		prefixConflict:   *prefixConflict,
		splitScope:       *splitScope,
		nonIdempotent:    *nonIdempotent,
		serializeReaders: *serializeReaders,
		unavailableClass: *unavailable,
		open:             map[string]openClaim{},
		closed:           map[string]bool{},
		writeOpen:        map[string]int{},
	}

	if *httpAddr != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/log", func(w http.ResponseWriter, r *http.Request) {
			p.mu.Lock()
			out, _ := json.Marshal(p.log)
			p.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(out)
		})
		mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
		go func() { _ = http.ListenAndServe(*httpAddr, mux) }()
	}

	lis, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Fatalf("listen %s: %v", *grpcAddr, err)
	}
	srv := grpc.NewServer()
	genv1.RegisterClaimProducerServer(srv, p)
	log.Printf("custom claim producer %q listening on %s (semantics=%s)", *name, *grpcAddr, *semantics)
	if err := srv.Serve(lis); err != nil {
		log.Fatal(err)
	}
}
