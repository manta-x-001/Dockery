import { api } from './api';

export interface ImageSearchItem {
  repo: string;
  tag: string;
  /** repo:tag */
  image: string;
  digest: string;
  git_repo?: string;
  git_commit?: string;
  git_branch?: string;
  git_tag?: string;
  git_hash?: string;
  indexed_at: number;
}

export interface ImageSearchResponse {
  items: ImageSearchItem[];
  total: number;
  page: number;
  page_size: number;
}

export interface ImageSearchParams {
  'git.repo'?: string;
  'git.commit'?: string;
  'git.hash'?: string;
  'git.branch'?: string;
  'git.tag'?: string;
  /** Substring match for repo / branch / tag (UI keyword search). */
  fuzzy?: boolean;
  page?: number;
  pageSize?: number;
}

export function searchImages(p: ImageSearchParams): Promise<ImageSearchResponse> {
  const qs = new URLSearchParams();
  for (const k of ['git.repo', 'git.commit', 'git.hash', 'git.branch', 'git.tag'] as const) {
    const v = p[k]?.trim();
    if (v) qs.set(k, v);
  }
  if (p.fuzzy) qs.set('fuzzy', 'true');
  if (p.page !== undefined) qs.set('page', String(p.page));
  if (p.pageSize !== undefined) qs.set('page_size', String(p.pageSize));
  return api.get<ImageSearchResponse>(`/api/images/search?${qs}`);
}
