package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func main() {
	addr := flag.String("addr", "", "host:port of the service")
	method := flag.String("method", "", "fully-qualified gRPC method, e.g. /rimsky.v1.Executor/Capabilities")
	flag.Parse()

	cc, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Printf("DIAL_ERROR %v\n", err)
		os.Exit(3)
	}
	defer func() { _ = cc.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err = cc.Invoke(ctx, *method, &emptypb.Empty{}, &emptypb.Empty{})
	if err == nil {
		fmt.Printf("OK %s\n", *method)
		return
	}
	st, _ := status.FromError(err)
	fmt.Printf("%s %s :: %s\n", st.Code().String(), *method, st.Message())
}
