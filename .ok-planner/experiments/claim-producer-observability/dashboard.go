package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

var fails int

func ok(format string, a ...any)  { fmt.Printf("PASS  "+format+"\n", a...) }
func bad(format string, a ...any) { fails++; fmt.Printf("FAIL  "+format+"\n", a...) }

func check(cond bool, format string, a ...any) {
	if cond {
		ok(format, a...)
		return
	}
	bad(format, a...)
}

func dump(label string, m interface{ String() string }) {
	fmt.Printf("    %s: %s\n", label, m.String())
}

func main() {
	endpoint := flag.String("endpoint", "127.0.0.1:19450", "claim producer gRPC endpoint")
	selector := flag.String("pick-selector", "@queue", "pick-policy selector the producer is configured with")
	flag.Parse()

	ctx := context.Background()
	conn, err := grpc.NewClient(*endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", *endpoint, err)
		os.Exit(2)
	}
	defer conn.Close()

	cp := genv1.NewClaimProducerClient(conn)
	obs := genv1.NewClaimProducerObservabilityClient(conn)

	fmt.Println("--- the producer declares what a dashboard may ask it for")
	caps, err := obs.Capabilities(ctx, &genv1.GetClaimProducerCapabilitiesRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Capabilities: %v\n", err)
		os.Exit(1)
	}
	dump("capabilities", caps)
	check(caps.GetSupportsClaimGet(), "the producer declares claim detail is fetchable")
	check(caps.GetSupportsClaimStream(), "the producer declares claim state is streamable")
	check(caps.GetSupportsListClaims(), "the producer declares its claim inventory is listable")
	views := map[string]*genv1.AdminViewDecl{}
	for _, v := range caps.GetAdminViews() {
		views[v.GetName()] = v
	}
	check(len(views) == 2, "the producer declares 2 admin views, got %d", len(views))
	for _, name := range []string{"pick_policies", "policy_items"} {
		v, present := views[name]
		if !present {
			bad("the producer declares an admin view named %q", name)
			continue
		}
		ok("the producer declares an admin view named %q titled %q", name, v.GetTitle())
	}
	if v, present := views["policy_items"]; present {
		check(len(v.GetParams()) == 1 && v.GetParams()[0].GetRequired(),
			"the policy_items view declares its required parameter, so a dashboard knows what to prompt for")
	}

	fmt.Println("--- a claim is taken from the producer's configured pick policy")
	queueID := uuid.NewString()
	queueOut, err := cp.Open(ctx, &genv1.OpenRequest{
		ClaimId: queueID, ProducerName: "claim-producer-filesystem",
		Selector: *selector, Intent: "rw", Alias: "queued",
	})
	if err != nil {
		bad("Open %s: %v", *selector, err)
	} else if queueOut.GetAcquired() == nil {
		bad("Open %s returned Unavailable", *selector)
	} else {
		ok("a claim on the pick policy %q was acquired", *selector)
	}

	fmt.Println("--- three claims are opened through the producer's own protocol")
	ids := make([]string, 0, 3)
	scopes := make(map[string][]byte)
	addrs := make(map[string][]byte)
	for i := 0; i < 3; i++ {
		id := uuid.NewString()
		sel := fmt.Sprintf("data/case-%d", i)
		out, oerr := cp.Open(ctx, &genv1.OpenRequest{
			ClaimId: id, ProducerName: "claim-producer-filesystem",
			Selector: sel, Intent: "rw", Alias: "held",
		})
		if oerr != nil {
			bad("Open %s: %v", sel, oerr)
			continue
		}
		acq := out.GetAcquired()
		if acq == nil {
			bad("Open %s returned Unavailable", sel)
			continue
		}
		ids = append(ids, id)
		scopes[id] = acq.GetClaimScope()
		addrs[id] = acq.GetAddress()
	}
	check(len(ids) == 3, "three claims are open, got %d", len(ids))

	fmt.Println("--- the inventory paginates")
	page1, err := obs.ListClaims(ctx, &genv1.ListClaimsRequest{Limit: 2})
	if err != nil {
		bad("ListClaims page 1: %v", err)
	} else {
		dump("page 1", page1)
		check(len(page1.GetClaims()) == 2, "the first page carries the requested 2 claims, got %d", len(page1.GetClaims()))
		check(page1.GetNextCursor() != "", "the first page carries a cursor for the next one")
		page2, perr := obs.ListClaims(ctx, &genv1.ListClaimsRequest{Limit: 2, Cursor: page1.GetNextCursor()})
		if perr != nil {
			bad("ListClaims page 2: %v", perr)
		} else {
			dump("page 2", page2)
			seen := map[string]bool{}
			for _, c := range page1.GetClaims() {
				seen[c.GetClaimId()] = true
			}
			overlap := false
			for _, c := range page2.GetClaims() {
				if seen[c.GetClaimId()] {
					overlap = true
				}
				seen[c.GetClaimId()] = true
			}
			check(!overlap, "the second page repeats no claim from the first")
			all := 0
			for _, id := range ids {
				if seen[id] {
					all++
				}
			}
			check(all == 3, "walking the cursor reaches all 3 open claims, reached %d", all)
		}
	}

	if len(ids) == 0 {
		os.Exit(reportExit())
	}
	target := ids[0]

	fmt.Println("--- a claim's full detail is fetchable")
	detail, err := obs.GetClaim(ctx, &genv1.GetClaimRequest{ClaimId: target})
	if err != nil {
		bad("GetClaim: %v", err)
	} else {
		dump("detail", detail)
		check(detail.GetState() == genv1.ClaimState_OPEN, "the claim reads as OPEN, got %s", detail.GetState())
		check(detail.GetOpenedAt() != nil, "the detail carries the time the claim opened")
		check(structHas(detail.GetScope()), "the detail carries the claim's scope")
		check(len(detail.GetHistory()) > 0, "the detail carries the claim's event history")
	}

	fmt.Println("--- a live state change arrives on the stream")
	stream, err := obs.StreamClaim(ctx, &genv1.StreamClaimRequest{ClaimId: target})
	if err != nil {
		bad("StreamClaim: %v", err)
		os.Exit(reportExit())
	}
	first, err := stream.Recv()
	if err != nil {
		bad("StreamClaim first event: %v", err)
		os.Exit(reportExit())
	}
	dump("stream event (snapshot)", first)
	ok("the stream replays the claim's state before the change")

	if _, cerr := cp.Commit(ctx, &genv1.CommitRequest{
		ClaimId: target, ClaimScope: scopes[target], Address: addrs[target],
	}); cerr != nil {
		bad("Commit: %v", cerr)
	} else {
		ok("the claim is committed through the producer while the stream is open")
	}

	for {
		ev, rerr := stream.Recv()
		if rerr != nil {
			bad("StreamClaim after commit: %v", rerr)
			break
		}
		dump("stream event", ev)
		if strings.Contains(strings.ToLower(ev.GetCategory()), "terminal") ||
			strings.Contains(strings.ToLower(ev.GetMessage()), "commit") ||
			strings.Contains(strings.ToLower(ev.GetEventId()), "commit") {
			ok("the commit reached the open stream as a live claim-state change")
			break
		}
	}

	after, err := obs.GetClaim(ctx, &genv1.GetClaimRequest{ClaimId: target})
	if err != nil {
		bad("GetClaim after commit: %v", err)
	} else {
		check(after.GetState() == genv1.ClaimState_COMMITTED,
			"the claim now reads as COMMITTED, got %s", after.GetState())
	}

	fmt.Println("--- the producer's own admin views render")
	pv, err := obs.GetAdminView(ctx, &genv1.GetAdminViewRequest{ViewName: "pick_policies"})
	if err != nil {
		bad("GetAdminView pick_policies: %v", err)
	} else {
		body, _ := protojson.Marshal(pv)
		fmt.Printf("    pick_policies: %s\n", string(body))
		check(len(pv.GetSchema().GetColumns()) > 0, "the view carries the column schema a dashboard renders from")
		check(pv.GetRenderHint() != "", "the view carries a render hint, got %q", pv.GetRenderHint())
		check(strings.Contains(string(body), *selector),
			"the view's rows name the configured pick policy %q", *selector)
	}

	params, _ := structpb.NewStruct(map[string]any{"selector": *selector})
	iv, err := obs.GetAdminView(ctx, &genv1.GetAdminViewRequest{ViewName: "policy_items", Params: params})
	if err != nil {
		bad("GetAdminView policy_items: %v", err)
	} else {
		body, _ := protojson.Marshal(iv)
		fmt.Printf("    policy_items: %s\n", string(body))
		check(strings.Contains(string(body), "job-"), "the parameterised view lists the items in that policy")
	}

	if _, err := obs.GetAdminView(ctx, &genv1.GetAdminViewRequest{ViewName: "no-such-view"}); err != nil {
		ok("an undeclared view name is refused rather than fabricated")
	} else {
		bad("an undeclared view name returned a view")
	}

	os.Exit(reportExit())
}

func structHas(s *structpb.Struct) bool {
	return s != nil && len(s.GetFields()) > 0
}

func reportExit() int {
	fmt.Println()
	if fails == 0 {
		fmt.Println("PROBE PASS")
		return 0
	}
	fmt.Printf("PROBE FAIL (%d)\n", fails)
	return 1
}
