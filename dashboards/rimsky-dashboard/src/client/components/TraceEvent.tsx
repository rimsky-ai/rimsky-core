import type { TraceEvent as Trace } from '../types';
import { ErrorBlock } from './ErrorBlock';
import { LogLine } from './LogLine';
import { ToolCallInspector } from './ToolCallInspector';

export function TraceEventView({ event }: { event: Trace }) {
  if (event.category === 'tool_call') return <ToolCallInspector event={event} />;
  if (event.category === 'error' || event.severity === 'ERROR') return <ErrorBlock event={event} />;
  return <LogLine event={event} />;
}
