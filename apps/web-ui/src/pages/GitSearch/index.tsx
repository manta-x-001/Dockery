/**
 * Git search — find which images were built from a given repository,
 * commit, branch or tag, using the git.* labels indexed at push time.
 */

import { Link, useSearchParams } from 'react-router-dom';
import { useState } from 'react';

import { useViewModel } from 'bizify';
import { GitSearchViewModel } from './view-model';
import { SmartSearchBar } from '@/components/common/SmartSearchBar';
import { formatGitQuery, parseGitQuery, type GitFilters } from '@/lib/git-query';
import { formatDateTime } from '@/utils';
import { Button } from '@/components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';

export default function GitSearch() {
  const vm = useViewModel(GitSearchViewModel);
  const s = vm.useSnapshot();
  const [, setSearchParams] = useSearchParams();

  const [raw, setRaw] = useState(() => {
    const p = new URLSearchParams(window.location.search);
    return formatGitQuery(parseGitQuery(Array.from(p, ([k, v]) => `${k}:${v}`).join(' ')).filters);
  });

  const search = async (filters: GitFilters) => {
    vm.setFilters(filters);
    setSearchParams(new URLSearchParams(vm.toQueryString()), { replace: true });
    await vm.run(0);
  };

  const pageCount = Math.max(1, Math.ceil(s.total / vm.pageSize));
  const short = (v?: string) => (v ? v.slice(0, 12) : '-');

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-bold">Git search</h2>
        <p className="text-sm text-muted-foreground mt-1">
          Find images by the <code>git.*</code> labels baked in at build time. Type <code>git.</code> for qualifiers; all filters are combined.
        </p>
      </div>

      <SmartSearchBar
        value={raw}
        onChange={setRaw}
        onGitSearch={search}
        placeholder="git.repo:github.com/acme/app git.branch:main …"
        className="max-w-2xl"
      />

      {s.error && <p className="text-sm text-destructive">{s.error}</p>}

      {s.searched && !s.error && (
        <>
          <p className="text-sm text-muted-foreground">
            {s.total} {s.total === 1 ? 'image' : 'images'} found
          </p>
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="px-4">Image</TableHead>
                  <TableHead className="px-4">Repository</TableHead>
                  <TableHead className="px-4">Commit</TableHead>
                  <TableHead className="px-4">Branch</TableHead>
                  <TableHead className="px-4">Git tag</TableHead>
                  <TableHead className="px-4">Indexed</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {s.items.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={6} className="text-center py-8 text-muted-foreground">
                      No images match.
                    </TableCell>
                  </TableRow>
                )}
                {s.items.map((it) => (
                  <TableRow key={`${it.repo}:${it.tag}`}>
                    <TableCell className="px-4 font-mono text-sm">
                      <Link
                        to={`/tag-list/${encodeURIComponent(it.repo)}`}
                        className="hover:text-primary hover:underline underline-offset-4"
                        title={it.digest}
                      >
                        {it.image}
                      </Link>
                    </TableCell>
                    <TableCell className="px-4 text-sm text-muted-foreground">{it.git_repo || '-'}</TableCell>
                    <TableCell className="px-4 font-mono text-sm text-muted-foreground" title={it.git_commit}>
                      {short(it.git_commit)}
                    </TableCell>
                    <TableCell className="px-4 text-sm text-muted-foreground">{it.git_branch || '-'}</TableCell>
                    <TableCell className="px-4 text-sm text-muted-foreground">{it.git_tag || '-'}</TableCell>
                    <TableCell className="px-4 text-sm text-muted-foreground tabular-nums">
                      {formatDateTime(new Date(it.indexed_at * 1000).toISOString())}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {pageCount > 1 && (
            <div className="flex items-center justify-end gap-2 text-sm">
              <Button variant="outline" size="sm" disabled={s.page === 0 || s.loading} onClick={() => vm.run(s.page - 1)}>
                Previous
              </Button>
              <span className="text-muted-foreground">
                Page {s.page + 1} / {pageCount}
              </span>
              <Button
                variant="outline"
                size="sm"
                disabled={s.page >= pageCount - 1 || s.loading}
                onClick={() => vm.run(s.page + 1)}
              >
                Next
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
