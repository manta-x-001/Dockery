/**
 * Pure helpers turning the flat /api/namespaces list into a tree.
 * Kept free of React so the shape can be unit-tested.
 */

import type { NamespaceView } from '@/services/namespace.service';

export interface TreeNode {
  path: string;
  /** Last path segment. */
  name: string;
  explicit: boolean;
  description?: string;
  repoCount: number;
  children: TreeNode[];
}

export function buildTree(items: NamespaceView[]): TreeNode[] {
  const byPath = new Map<string, TreeNode>();
  for (const it of items) {
    byPath.set(it.path, {
      path: it.path,
      name: it.path.slice(it.path.lastIndexOf('/') + 1),
      explicit: it.explicit,
      description: it.description,
      repoCount: it.repo_count,
      children: [],
    });
  }
  const roots: TreeNode[] = [];
  const sorted = [...byPath.values()].sort((a, b) => a.path.localeCompare(b.path));
  for (const node of sorted) {
    const slash = node.path.lastIndexOf('/');
    // A node whose parent isn't visible to this user is shown at the
    // top level rather than dropped.
    const parent = slash >= 0 ? byPath.get(node.path.slice(0, slash)) : undefined;
    (parent ? parent.children : roots).push(node);
  }
  return roots;
}

/** Ancestors of `path`, excluding itself: "a/b/c" → ["a", "a/b"]. */
export function ancestorsOf(path: string): string[] {
  const segs = path.split('/');
  return segs.slice(0, -1).map((_, i) => segs.slice(0, i + 1).join('/'));
}
