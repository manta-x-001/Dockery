import { describe, expect, it } from 'vitest';
import { ancestorsOf, buildTree } from './namespace-tree';

const ns = (path: string, explicit = false, repo_count = 0) => ({ path, explicit, repo_count });

describe('buildTree', () => {
  it('nests children under their parent', () => {
    const tree = buildTree([ns('org', true, 3), ns('org/team', true, 2), ns('other', false, 1)]);
    expect(tree.map((n) => n.path)).toEqual(['org', 'other']);
    expect(tree[0].children.map((n) => [n.path, n.name])).toEqual([['org/team', 'team']]);
  });

  it('keeps a node whose parent is not visible at the top level', () => {
    const tree = buildTree([ns('org/team', true)]);
    expect(tree.map((n) => n.path)).toEqual(['org/team']);
  });
});

describe('ancestorsOf', () => {
  it('lists parents shallowest first', () => {
    expect(ancestorsOf('a/b/c')).toEqual(['a', 'a/b']);
    expect(ancestorsOf('a')).toEqual([]);
  });
});
