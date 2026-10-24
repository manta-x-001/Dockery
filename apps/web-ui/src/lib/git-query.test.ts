import { describe, expect, it } from 'vitest';
import { formatGitQuery, hasGitFilter, parseGitQuery } from './git-query';

describe('parseGitQuery', () => {
  it('splits free text from git tokens', () => {
    expect(parseGitQuery('nginx git.repo:github.com/acme/app git.branch:main')).toEqual({
      text: 'nginx',
      filters: { 'git.repo': 'github.com/acme/app', 'git.branch': 'main' },
    });
  });
  it('keeps incomplete or unknown tokens as text', () => {
    const r = parseGitQuery('git.repo: foo:bar git.nope:x');
    expect(r.text).toBe('git.repo: foo:bar git.nope:x');
    expect(hasGitFilter(r.filters)).toBe(false);
  });
  it('round-trips through formatGitQuery', () => {
    const f = { 'git.commit': 'abcdef1', 'git.tag': 'v1' } as const;
    expect(parseGitQuery(formatGitQuery(f)).filters).toEqual(f);
  });
});
