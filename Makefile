.PHONY: proto-gen test build lint tidy

proto-gen:
	cd proto/v1 && protoc --go_out=gen --go_opt=paths=source_relative \
	  --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
	  node_executor.proto events.proto

test:
	go test ./...

build:
	go build ./...

lint:
	golangci-lint run

tidy:
	go mod tidy
