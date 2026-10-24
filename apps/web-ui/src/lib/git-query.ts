/**
 * GitHub-style search syntax: free text plus `git.<field>:value` tokens,
 * e.g. `nginx git.repo:github.com/acme/app git.branch:main`.
 */

export type GitField = 'git.repo' | 'git.commit' | 'git.hash' | 'git.branch' | 'git.tag';

export const GIT_FIELDS: { key: GitField; label: string; example: string }[] = [
  { key: 'git.repo', label: 'Source repository', example: 'github.com/acme/app' },
  { key: 'git.commit', label: 'Commit SHA (7+ chars)', example: 'a1b2c3d' },
  { key: 'git.hash', label: 'Hash (7+ chars)', example: 'a1b2c3d4e5f6' },
  { key: 'git.branch', label: 'Branch name', example: 'main' },
  { key: 'git.tag', label: 'Git tag', example: 'v1.2.0' },
];

export type GitFilters = Partial<Record<GitField, string>>;

const FIELD_KEYS = new Set<string>(GIT_FIELDS.map((f) => f.key));

/** Splits a query into recognised git filters and the remaining free text. */
export function parseGitQuery(q: string): { text: string; filters: GitFilters } {
  const filters: GitFilters = {};
  const rest: string[] = [];
  for (const tok of q.split(/\s+/).filter(Boolean)) {
    const i = tok.indexOf(':');
    if (i > 0 && FIELD_KEYS.has(tok.slice(0, i)) && tok.length > i + 1) {
      filters[tok.slice(0, i) as GitField] = tok.slice(i + 1);
    } else {
      rest.push(tok);
    }
  }
  return { text: rest.join(' '), filters };
}

export function hasGitFilter(f: GitFilters): boolean {
  return Object.keys(f).length > 0;
}

/** Inverse of parseGitQuery, used to prefill the box from a URL. */
export function formatGitQuery(filters: GitFilters): string {
  return GIT_FIELDS.filter((f) => filters[f.key])
    .map((f) => `${f.key}:${filters[f.key]}`)
    .join(' ');
}
