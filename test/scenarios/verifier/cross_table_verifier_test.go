// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package verifier

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	tmplspec "github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/peer"
)

type recordingValidationServer struct {
	genv1.UnimplementedValidationServer
	mu       sync.Mutex
	received []*genv1.ExecutorContext
}

func (s *recordingValidationServer) Validate(_ context.Context, req *genv1.ValidateRequest) (*genv1.ValidateResponse, error) {
	s.mu.Lock()
	s.received = append(s.received, req.GetExecutor())
	s.mu.Unlock()
	return &genv1.ValidateResponse{Valid: true}, nil
}

type singleValidatorRegistry struct {
	name   string
	client runtime.ValidationClient
}

func (r *singleValidatorRegistry) Get(name string) (runtime.ValidationClient, bool) {
	if name != r.name {
		return nil, false
	}
	return r.client, true
}

func (r *singleValidatorRegistry) All() []runtime.ValidationClient {
	return []runtime.ValidationClient{r.client}
}

func TestCrossTableVerifier_ClaimAliasesPassThroughExecutorContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	rec := &recordingValidationServer{}
	genv1.RegisterValidationServer(srv, rec)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	client, err := peer.DialValidation(ctx, "content-checker", "grpc://"+lis.Addr().String(), peer.TLSModeOff, []string{"executor"})
	require.NoError(t, err)
	t.Cleanup(client.Close)

	reg := &singleValidatorRegistry{name: "content-checker", client: client}

	tpl := tmplspec.TemplateSpec{
		Name: "cross-table-verifier", Version: "1",
		Nodes: []tmplspec.TemplateNodeDef{
			{
				Type: "worker", Executor: "content-checker",
				ClaimProducers: []tmplspec.NodeClaimProducerRef{
					{Name: "content", Selector: "/a", Intent: "rw", Alias: "primary"},
					{Name: "content", Selector: "/b", Intent: "rw", Alias: "secondary"},
					{Name: "content", Selector: "/c", Intent: "rw", Alias: "audit"},
				},
			},
		},
	}

	out, err := runtime.RunValidationPipeline(ctx, reg, tpl, "tpl-cross-table", runtime.UnreachableValidatorPermissiveWarn, nil)
	require.NoError(t, err)
	require.Empty(t, out.Errors, "well-formed executor+claims must validate cleanly")

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.received, 1, "the executor role check must reach the wire exactly once")
	got := rec.received[0].GetClaimAliases()
	require.ElementsMatch(t, []string{"primary", "secondary", "audit"}, got,
		"the node's declared claim_producer aliases must arrive verbatim in the wire ExecutorContext")
	require.Equal(t, "worker", rec.received[0].GetNodeAlias())
}
