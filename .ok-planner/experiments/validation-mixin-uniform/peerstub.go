package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

var (
	peerName  string
	peerKind  string
	peerRoles []string
)

type validationSvc struct {
	genv1.UnimplementedValidationServer
}

func contextDetail(req *genv1.ValidateRequest) string {
	switch {
	case req.GetExecutor() != nil:
		return "node_alias=" + req.GetExecutor().GetNodeAlias()
	case req.GetPublisher() != nil:
		return "publisher_name=" + req.GetPublisher().GetPublisherName()
	case req.GetClaimProducer() != nil:
		return "producer_name=" + req.GetClaimProducer().GetProducerName()
	case req.GetLifecycleSubscriber() != nil:
		return "subscriber_name=" + req.GetLifecycleSubscriber().GetSubscriberName()
	}
	return "no_context"
}

func (validationSvc) Validate(_ context.Context, req *genv1.ValidateRequest) (*genv1.ValidateResponse, error) {
	fmt.Fprintf(os.Stderr, "peerstub %s (%s): Validate role=%s %s\n", peerName, peerKind, req.GetRole(), contextDetail(req))
	return &genv1.ValidateResponse{
		Valid: true,
		Warnings: []*genv1.ValidationFinding{{
			Class:   "mixin_consulted",
			Message: fmt.Sprintf("peer %q (%s mix-in) was consulted for role %q with %s", peerName, peerKind, req.GetRole(), contextDetail(req)),
			Path:    "/" + peerKind,
		}},
	}, nil
}

type executorSvc struct {
	genv1.UnimplementedExecutorServer
}

func (executorSvc) Execute(context.Context, *genv1.ExecuteRequest) (*genv1.Outcome, error) {
	delta, _ := structpb.NewStruct(map[string]any{"peerstub": true})
	return &genv1.Outcome{Outcome: &genv1.Outcome_Success{Success: &genv1.Success{AttributesDelta: delta}}}, nil
}

type executorObsSvc struct {
	genv1.UnimplementedExecutorObservabilityServer
}

func (executorObsSvc) Capabilities(context.Context, *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{ValidationSupportedRoles: peerRoles}, nil
}

type publisherSvc struct {
	genv1.UnimplementedPublisherServer
}

func (publisherSvc) Capabilities(context.Context, *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	return &genv1.PublisherCapabilities{
		SupportedKinds:           []*genv1.PublisherKindCapability{{Kind: "probe"}},
		Protocols:                []string{"publisher", "validation"},
		ValidationSupportedRoles: peerRoles,
	}, nil
}

func (publisherSvc) Subscribe(context.Context, *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	return &genv1.SubscribeResponse{}, nil
}

func (publisherSvc) Unsubscribe(context.Context, *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	return &genv1.UnsubscribeResponse{}, nil
}

func (publisherSvc) ListSubscriptions(context.Context, *emptypb.Empty) (*genv1.ListSubscriptionsResponse, error) {
	return &genv1.ListSubscriptionsResponse{}, nil
}

func main() {
	port := flag.Int("port", 0, "TCP port to listen on")
	flag.StringVar(&peerName, "name", "peer", "peer name as declared in rimsky.yml")
	flag.StringVar(&peerKind, "kind", "executor", "primary protocol: executor | publisher")
	roles := flag.String("roles", "", "comma-separated validation_supported_roles advertised in the capabilities handshake")
	flag.Parse()

	for _, r := range splitCSV(*roles) {
		peerRoles = append(peerRoles, r)
	}
	if len(peerRoles) == 0 {
		peerRoles = []string{peerKind}
	}

	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", *port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "peerstub listen: %v\n", err)
		os.Exit(1)
	}
	srv := grpc.NewServer()
	genv1.RegisterValidationServer(srv, validationSvc{})
	switch peerKind {
	case "executor":
		genv1.RegisterExecutorServer(srv, executorSvc{})
		genv1.RegisterExecutorObservabilityServer(srv, executorObsSvc{})
	case "publisher":
		genv1.RegisterPublisherServer(srv, publisherSvc{})
	default:
		fmt.Fprintf(os.Stderr, "peerstub: unknown -kind %q\n", peerKind)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "peerstub %s (%s) listening on %s roles=%v\n", peerName, peerKind, lis.Addr().String(), peerRoles)
	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "peerstub serve: %v\n", err)
		os.Exit(1)
	}
}

func splitCSV(s string) []string {
	out := []string{}
	cur := ""
	for _, c := range s {
		if c == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
