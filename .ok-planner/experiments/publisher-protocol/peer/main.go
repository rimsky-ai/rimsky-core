// A third-party rimsky publisher, built the same way as the
// permissive-peer-build experiment's executor peer: its own Go module whose
// only rimsky requirement is the permissively licensed protocols module.
//
// It serves the publisher protocol (Capabilities, Subscribe, Unsubscribe,
// ListSubscriptions), keeps its subscriptions in its own process, and feeds
// messages into the workflows it is subscribed for by posting them to the
// control API.
//
// Its own HTTP side lets the run drive and inspect it:
//
//	GET /state    -> call counts and the subscriptions it currently holds
//	GET /publish  -> feed one message per held subscription
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/rimsky-ai/rimsky-core/lib/protocols/peerauth"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const publisherKind = "tick"

var configSchema = []byte(`{
  "type": "object",
  "properties": {"label": {"type": "string"}},
  "additionalProperties": false
}`)

type subscription struct {
	ID          string
	InstanceID  string
	Kind        string
	Config      []byte
	MessageType string
	StartedAt   time.Time
}

type publisher struct {
	genv1.UnimplementedPublisherServer

	mu       sync.Mutex
	subs     map[string]subscription
	counts   map[string]int
	sequence int
}

func newPublisher() *publisher {
	return &publisher{subs: map[string]subscription{}, counts: map[string]int{}}
}

func (p *publisher) Capabilities(context.Context, *emptypb.Empty) (*genv1.PublisherCapabilities, error) {
	p.bump("capabilities_calls")
	return &genv1.PublisherCapabilities{
		SupportedKinds: []*genv1.PublisherKindCapability{{Kind: publisherKind, ConfigSchema: configSchema}},
		Protocols:      []string{"publisher"},
	}, nil
}

func (p *publisher) Subscribe(_ context.Context, req *genv1.SubscribeRequest) (*genv1.SubscribeResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts["subscribe_calls"]++
	p.subs[req.GetPublisherSubscriptionId()] = subscription{
		ID:          req.GetPublisherSubscriptionId(),
		InstanceID:  req.GetInstanceId(),
		Kind:        req.GetKind(),
		Config:      req.GetResolvedConfig(),
		MessageType: req.GetMessageType(),
		StartedAt:   time.Now().UTC(),
	}
	log.Printf("subscribe id=%s instance=%s kind=%s type=%s config=%s",
		req.GetPublisherSubscriptionId(), req.GetInstanceId(), req.GetKind(),
		req.GetMessageType(), string(req.GetResolvedConfig()))
	return &genv1.SubscribeResponse{}, nil
}

func (p *publisher) Unsubscribe(_ context.Context, req *genv1.UnsubscribeRequest) (*genv1.UnsubscribeResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts["unsubscribe_calls"]++
	delete(p.subs, req.GetPublisherSubscriptionId())
	log.Printf("unsubscribe id=%s", req.GetPublisherSubscriptionId())
	return &genv1.UnsubscribeResponse{}, nil
}

func (p *publisher) ListSubscriptions(context.Context, *emptypb.Empty) (*genv1.ListSubscriptionsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts["list_calls"]++
	out := &genv1.ListSubscriptionsResponse{}
	for _, s := range p.subs {
		out.Subscriptions = append(out.Subscriptions, &genv1.PublisherSubscriptionDescriptor{
			PublisherSubscriptionId: s.ID,
			InstanceId:              s.InstanceID,
			Kind:                    s.Kind,
			ResolvedConfig:          s.Config,
			MessageType:             s.MessageType,
			StartedAt:               timestamppb.New(s.StartedAt),
		})
	}
	return out, nil
}

func (p *publisher) bump(key string) {
	p.mu.Lock()
	p.counts[key]++
	p.mu.Unlock()
}

func (p *publisher) snapshot() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	subs := make([]map[string]any, 0, len(p.subs))
	for _, s := range p.subs {
		subs = append(subs, map[string]any{
			"publisher_subscription_id": s.ID,
			"instance_id":               s.InstanceID,
			"kind":                      s.Kind,
			"message_type":              s.MessageType,
			"config":                    string(s.Config),
		})
	}
	counts := map[string]int{}
	for k, v := range p.counts {
		counts[k] = v
	}
	return map[string]any{"counts": counts, "subscriptions": subs}
}

func (p *publisher) publish(endpoint string) (int, error) {
	p.mu.Lock()
	p.sequence++
	seq := p.sequence
	subs := make([]subscription, 0, len(p.subs))
	for _, s := range p.subs {
		subs = append(subs, s)
	}
	p.mu.Unlock()

	sent := 0
	for _, s := range subs {
		envelope := map[string]any{
			"type":                      s.MessageType,
			"payload":                   map[string]any{"n": seq},
			"sender":                    "publisher-peer",
			"publisher_subscription_id": s.ID,
		}
		raw, err := json.Marshal(envelope)
		if err != nil {
			return sent, err
		}
		url := endpoint + "/v1/instances/" + s.InstanceID + "/messages"
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			return sent, err
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("Idempotency-Key", fmt.Sprintf("publisher-peer-%s-%d", s.ID, seq))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return sent, err
		}
		body := make([]byte, 512)
		n, _ := resp.Body.Read(body)
		resp.Body.Close()
		log.Printf("publish id=%s instance=%s status=%d body=%s", s.ID, s.InstanceID, resp.StatusCode, string(body[:n]))
		if resp.StatusCode >= 300 {
			return sent, fmt.Errorf("control api returned %d: %s", resp.StatusCode, string(body[:n]))
		}
		sent++
	}
	return sent, nil
}

func envPort(key string, dflt int) int {
	v := os.Getenv(key)
	if v == "" {
		return dflt
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("publisher-peer: %s=%q is not a port number", key, v)
	}
	return n
}

func main() {
	endpoint := os.Getenv("RIMSKY_ENDPOINT")
	if endpoint == "" {
		log.Fatal("publisher-peer: RIMSKY_ENDPOINT is required")
	}
	ctx := context.Background()
	p := newPublisher()

	srv, identity, err := peerauth.NewGRPCServer(ctx, "publisher-peer")
	if err != nil {
		log.Fatalf("publisher-peer: peer-auth setup failed: %v", err)
	}
	identity.StartMaintain(ctx, "publisher-peer")
	genv1.RegisterPublisherServer(srv, p)

	mux := http.NewServeMux()
	mux.HandleFunc("/state", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(p.snapshot())
	})
	mux.HandleFunc("/publish", func(w http.ResponseWriter, _ *http.Request) {
		sent, err := p.publish(endpoint)
		w.Header().Set("content-type", "application/json")
		out := map[string]any{"sent": sent}
		if err != nil {
			out["error"] = err.Error()
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	go func() {
		if err := http.ListenAndServe(fmt.Sprintf(":%d", envPort("PEER_HTTP_PORT", 9501)), mux); err != nil {
			log.Fatalf("publisher-peer: http: %v", err)
		}
	}()

	addr := fmt.Sprintf("0.0.0.0:%d", envPort("PEER_PORT", 9500))
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("publisher-peer: listen %s: %v", addr, err)
	}
	log.Printf("publisher-peer listening on %s (peer_auth_mtls=%v)", addr, identity.Enabled())
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("publisher-peer: serve: %v", err)
	}
}
