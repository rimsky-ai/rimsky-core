// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import type { TraceEvent as Trace } from '../types';
import { ErrorBlock } from './ErrorBlock';
import { LogLine } from './LogLine';
import { ToolCallInspector } from './ToolCallInspector';

export function TraceEventView({ event }: { event: Trace }) {
  if (event.category === 'tool_call') return <ToolCallInspector event={event} />;
  if (event.category === 'error' || event.severity === 'ERROR') return <ErrorBlock event={event} />;
  return <LogLine event={event} />;
}
