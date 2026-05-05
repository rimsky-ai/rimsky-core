// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useNavigate } from 'react-router-dom';
import type { CascadeNode } from '../types';

const stateFill: Record<string, string> = {
  fresh: '#e5e7eb',
  stale: '#fde68a',
  running: '#bfdbfe',
  failed: '#fecaca',
};

interface Layout {
  layer: number;
  index: number;
  total: number;
}

// Layered topo sort: each node's layer is max(layer(deps)) + 1.
function computeLayers(nodes: CascadeNode[]): Map<string, Layout> {
  const byType = new Map(nodes.map((n) => [n.node_type, n]));
  const layer = new Map<string, number>();
  function resolve(t: string, seen: Set<string>): number {
    if (layer.has(t)) return layer.get(t)!;
    if (seen.has(t)) return 0;
    seen.add(t);
    const node = byType.get(t);
    if (!node || node.edges_in.length === 0) {
      layer.set(t, 0);
      return 0;
    }
    const dep = node.edges_in.reduce((m, d) => Math.max(m, resolve(d, seen) + 1), 0);
    layer.set(t, dep);
    return dep;
  }
  for (const n of nodes) resolve(n.node_type, new Set());

  const groups = new Map<number, CascadeNode[]>();
  for (const n of nodes) {
    const l = layer.get(n.node_type) ?? 0;
    if (!groups.has(l)) groups.set(l, []);
    groups.get(l)!.push(n);
  }
  const out = new Map<string, Layout>();
  for (const [l, group] of groups) {
    group.forEach((n, i) => {
      out.set(n.node_type, { layer: l, index: i, total: group.length });
    });
  }
  return out;
}

const NODE_W = 140;
const NODE_H = 56;
const X_GAP = 200;
const Y_GAP = 80;

export function CascadeGraph({ nodes, instanceId }: { nodes: CascadeNode[]; instanceId: string }) {
  const navigate = useNavigate();
  if (nodes.length === 0) {
    return <p className="text-sm text-muted-foreground p-2">No nodes in this instance.</p>;
  }
  const layout = computeLayers(nodes);
  const maxLayer = Math.max(...Array.from(layout.values()).map((l) => l.layer));
  const maxLayerSize = Math.max(...Array.from(layout.values()).map((l) => l.total));
  const width = (maxLayer + 1) * X_GAP + 100;
  const height = maxLayerSize * Y_GAP + 60;

  const positions = new Map<string, { x: number; y: number }>();
  for (const n of nodes) {
    const l = layout.get(n.node_type)!;
    const x = l.layer * X_GAP + 40;
    const y = (l.index - (l.total - 1) / 2) * Y_GAP + height / 2 - NODE_H / 2;
    positions.set(n.node_type, { x, y });
  }

  return (
    <svg width={width} height={height} className="border rounded-md bg-muted/20">
      {/* edges */}
      {nodes.map((n) =>
        (n.edges_in ?? []).map((from) => {
          const a = positions.get(from);
          const b = positions.get(n.node_type);
          if (!a || !b) return null;
          return (
            <line
              key={`${from}->${n.node_type}`}
              x1={a.x + NODE_W}
              y1={a.y + NODE_H / 2}
              x2={b.x}
              y2={b.y + NODE_H / 2}
              stroke="#94a3b8"
              strokeWidth={1.5}
              markerEnd="url(#arrow)"
            />
          );
        }),
      )}
      <defs>
        <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
          <path d="M 0 0 L 10 5 L 0 10 z" fill="#94a3b8" />
        </marker>
      </defs>
      {/* nodes */}
      {nodes.map((n) => {
        const p = positions.get(n.node_type)!;
        const fill = stateFill[n.state] ?? '#fff';
        return (
          <g
            key={n.node_type}
            transform={`translate(${p.x},${p.y})`}
            className="cursor-pointer"
            onClick={() => navigate(`/instances/${instanceId}/nodes/${n.node_type}`)}
          >
            <rect
              width={NODE_W}
              height={NODE_H}
              rx={6}
              ry={6}
              fill={fill}
              stroke="#475569"
              strokeWidth={1}
            />
            <text x={10} y={20} fontSize={12} fontWeight={600} fill="#0f172a">
              {n.node_type}
            </text>
            <text x={10} y={38} fontSize={10} fill="#475569">
              {n.state}
            </text>
            {n.retry_counter > 0 && (
              <text x={NODE_W - 10} y={20} fontSize={10} fill="#dc2626" textAnchor="end">
                ↻ {n.retry_counter}
              </text>
            )}
          </g>
        );
      })}
    </svg>
  );
}
