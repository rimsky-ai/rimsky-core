// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Type declarations for @rimsky-ai/protocols. See index.js.

/** Absolute path to the directory holding the v1 .proto files. */
export declare const protoDir: string;

/**
 * Absolute path to a named .proto under proto/v1.
 * @example protoPath("executor.proto")
 */
export declare function protoPath(file: string): string;
