// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));

export const protoDir = join(here, "proto", "v1");

export function protoPath(file) {
  return join(protoDir, file);
}
