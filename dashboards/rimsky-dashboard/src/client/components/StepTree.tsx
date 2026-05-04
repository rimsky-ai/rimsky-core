import type { TraceEvent } from '../types';
import { Badge } from './ui/badge';

interface Node {
  id: string;
  start: TraceEvent;
  end?: TraceEvent;
  children: Node[];
}

// Build a step tree from step_started/step_completed/step_failed events.
// parent_event_id is canonical when present; otherwise step_id matches
// to the most recently-opened ancestor.
export function buildStepTree(events: TraceEvent[]): Node[] {
  const stepsById = new Map<string, Node>();
  const roots: Node[] = [];
  const stack: Node[] = [];
  for (const ev of events) {
    if (ev.category === 'step_started' || ev.category === 'subcall_started') {
      const id = (ev.attributes?.step_id ?? ev.attributes?.subcall_id ?? ev.event_id) as string;
      const node: Node = { id, start: ev, children: [] };
      stepsById.set(id, node);
      const parentId = ev.parent_event_id;
      const parentByPe = parentId ? stepsById.get(parentId) : undefined;
      const parentByStack = stack[stack.length - 1];
      const parent = parentByPe ?? parentByStack;
      if (parent) parent.children.push(node);
      else roots.push(node);
      stack.push(node);
    } else if (ev.category === 'step_completed' || ev.category === 'step_failed' || ev.category === 'subcall_completed') {
      const id = (ev.attributes?.step_id ?? ev.attributes?.subcall_id) as string | undefined;
      const node = id ? stepsById.get(id) : stack[stack.length - 1];
      if (node) node.end = ev;
      // pop the stack until we close the matched node
      const idx = id ? stack.findIndex((n) => n.id === id) : stack.length - 1;
      if (idx >= 0) stack.length = idx;
    }
  }
  return roots;
}

function StepNode({ node, depth = 0 }: { node: Node; depth?: number }) {
  const failed = node.end?.category === 'step_failed';
  const open = !node.end;
  const variant = failed ? 'error' : open ? 'info' : 'success';
  return (
    <div style={{ paddingLeft: depth * 16 }} className="py-1">
      <div className="flex items-center gap-2 text-sm">
        <Badge variant={variant}>{failed ? 'failed' : open ? 'running' : 'completed'}</Badge>
        <span className="font-mono">{node.id}</span>
        {node.start.message && <span className="text-muted-foreground">{node.start.message}</span>}
      </div>
      {node.children.map((c, i) => (
        <StepNode key={`${c.id}-${i}`} node={c} depth={depth + 1} />
      ))}
    </div>
  );
}

export function StepTree({ events }: { events: TraceEvent[] }) {
  const roots = buildStepTree(events);
  if (roots.length === 0) return <div className="text-sm text-muted-foreground p-2">No steps emitted.</div>;
  return (
    <div>
      {roots.map((r, i) => (
        <StepNode key={`${r.id}-${i}`} node={r} />
      ))}
    </div>
  );
}
