/**
 * @source: prototype-repo:src/features/zones/partition.ts
 * @diverged: true
 * @reason: generateZoneId moved to @crimefinder/shared/ids; ignore-pattern
 *          list extended to be config-overridable; ignore-pattern matching
 *          now strips trailing slashes so the "vendor/" convention works.
 */

import fs from "node:fs";
import path from "node:path";
import { generateZoneId } from "@crimefinder/shared";

export interface Zone {
  id: string;
  label: string;
  files: string[];
}

const DEFAULT_IGNORE_PATTERNS = [
  "node_modules",
  ".git",
  "dist",
  ".crimefinder",
  "build",
  "coverage",
];

const DEFAULT_MAX_FILES_PER_ZONE = 50;
const DEFAULT_SMALL_GROUP_THRESHOLD = 10;

export interface PartitionOptions {
  projectRoot: string;
  targetPath?: string;
  maxFilesPerZone?: number;
  smallGroupThreshold?: number;
  ignorePatterns?: string[];
}

// Ignore-patterns are matched against directory entry names (basename, no
// slash). Operators often write the convention "vendor/" in config to
// signal "directory", so we strip a trailing slash before building the
// match set — otherwise "vendor/" would never match the entry name
// "vendor".
function normalizeIgnorePatterns(patterns: string[]): string[] {
  return patterns.map((p) => (p.endsWith("/") ? p.slice(0, -1) : p));
}

export function partitionIntoZones(opts: PartitionOptions): Zone[] {
  const {
    projectRoot,
    targetPath,
    maxFilesPerZone = DEFAULT_MAX_FILES_PER_ZONE,
    smallGroupThreshold = DEFAULT_SMALL_GROUP_THRESHOLD,
    ignorePatterns = DEFAULT_IGNORE_PATTERNS,
  } = opts;
  const scanRoot = targetPath ? path.resolve(projectRoot, targetPath) : projectRoot;
  const ignoreSet = new Set(normalizeIgnorePatterns(ignorePatterns));

  const allFiles = collectFiles(scanRoot, projectRoot, ignoreSet);
  if (allFiles.length === 0) return [];

  const groups = groupByDirectory(allFiles);
  const zones: Zone[] = [];
  const smallGroups = new Map<string, Map<string, string[]>>();

  for (const [dir, files] of groups) {
    if (dir === ".") {
      zones.push(createZone("(root)", files));
      continue;
    }
    if (files.length > maxFilesPerZone) {
      const chunkCount = Math.ceil(files.length / maxFilesPerZone);
      const chunkSize = Math.ceil(files.length / chunkCount);
      for (let i = 0; i < chunkCount; i++) {
        const chunk = files.slice(i * chunkSize, (i + 1) * chunkSize);
        zones.push(createZone(`${dir} (${i + 1}/${chunkCount})`, chunk));
      }
    } else if (files.length < smallGroupThreshold) {
      const parent = path.dirname(dir);
      if (!smallGroups.has(parent)) smallGroups.set(parent, new Map());
      smallGroups.get(parent)!.set(dir, files);
    } else {
      zones.push(createZone(dir, files));
    }
  }

  for (const [parent, siblings] of smallGroups) {
    const allSiblingFiles: string[] = [];
    for (const [, files] of siblings) {
      allSiblingFiles.push(...files);
    }
    if (allSiblingFiles.length <= maxFilesPerZone) {
      const label = parent === "." ? "(root-dirs)" : parent;
      zones.push(createZone(label, allSiblingFiles));
    } else {
      for (const [dir, files] of siblings) {
        zones.push(createZone(dir, files));
      }
    }
  }

  zones.sort((a, b) => a.label.localeCompare(b.label));
  return zones;
}

function createZone(label: string, files: string[]): Zone {
  return {
    id: generateZoneId(label),
    label,
    files: [...files].sort(),
  };
}

function collectFiles(scanRoot: string, projectRoot: string, ignoreSet: Set<string>): string[] {
  const files: string[] = [];
  if (!fs.existsSync(scanRoot)) return files;

  function walk(dir: string): void {
    let entries: fs.Dirent[];
    try {
      entries = fs.readdirSync(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      if (ignoreSet.has(entry.name)) continue;
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.isFile()) files.push(path.relative(projectRoot, full));
    }
  }
  walk(scanRoot);
  return files;
}

function groupByDirectory(files: string[]): Map<string, string[]> {
  const groups = new Map<string, string[]>();
  for (const file of files) {
    const dir = path.dirname(file);
    if (!groups.has(dir)) groups.set(dir, []);
    groups.get(dir)!.push(file);
  }
  return groups;
}
