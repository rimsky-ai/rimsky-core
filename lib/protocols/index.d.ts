// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// @agent-contract: public TypeScript surface of @rimsky-ai/protocols — the
// runtime path helpers consumers use to resolve the shipped .proto files.

// @agent-contract: protoDir resolves to the directory holding the v1 .proto
// files; pass it as `includeDirs` to @grpc/proto-loader so cross-proto imports
// resolve.
export declare const protoDir: string;

// @agent-contract: protoPath resolves the absolute path of a named .proto file
// under proto/v1 (e.g. protoPath("executor.proto")).
export declare function protoPath(file: string): string;
