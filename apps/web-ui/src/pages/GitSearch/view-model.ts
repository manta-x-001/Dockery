/**
 * GitSearchViewModel — reverse lookup from git coordinates to images,
 * backed by GET /api/images/search.
 */

import { ViewModelBase } from 'bizify';
import { searchImages, type ImageSearchItem } from '@/services/image-search.service';
import { ApiError } from '@/services/api';

import { GIT_FIELDS, type GitField, type GitFilters } from '@/lib/git-query';

const PAGE_SIZE = 50;

interface State {
  form: Record<GitField, string>;
  items: ImageSearchItem[];
  total: number;
  page: number;
  searched: boolean;
  loading: boolean;
  error: string | null;
}

export class GitSearchViewModel extends ViewModelBase<State> {
  private reqSeq = 0;

  protected $data(): State {
    const params = new URLSearchParams(window.location.search);
    const form = Object.fromEntries(GIT_FIELDS.map((f) => [f.key, params.get(f.key) ?? ''])) as Record<
      GitField,
      string
    >;
    return { form, items: [], total: 0, page: 0, searched: false, loading: false, error: null };
  }

  protected onMount() {
    // A shared link carries its filters; run them straight away.
    if (this.hasFilter()) void this.run(0);
  }

  /** Replaces all filters (the search box is the only input). */
  setFilters(f: GitFilters) {
    this.data.form = Object.fromEntries(GIT_FIELDS.map((x) => [x.key, f[x.key] ?? ''])) as Record<GitField, string>;
  }

  hasFilter(): boolean {
    return GIT_FIELDS.some((f) => this.data.form[f.key].trim() !== '');
  }

  /** Query string mirroring the current filters, for the URL. */
  toQueryString(): string {
    const qs = new URLSearchParams();
    for (const f of GIT_FIELDS) {
      const v = this.data.form[f.key].trim();
      if (v) qs.set(f.key, v);
    }
    return qs.toString();
  }

  async run(page = 0) {
    if (!this.hasFilter()) {
      this.data.error = 'Enter at least one filter';
      return;
    }
    const mySeq = ++this.reqSeq;
    Object.assign(this.data, { loading: true, error: null });
    try {
      const resp = await searchImages({ ...this.data.form, fuzzy: true, page, pageSize: PAGE_SIZE });
      if (mySeq !== this.reqSeq) return;
      Object.assign(this.data, {
        items: resp.items,
        total: resp.total,
        page: resp.page,
        searched: true,
        loading: false,
      });
    } catch (err) {
      if (mySeq !== this.reqSeq) return;
      Object.assign(this.data, {
        loading: false,
        error: err instanceof ApiError ? err.message : 'Search failed',
      });
    }
  }

  get pageSize() {
    return PAGE_SIZE;
  }
}
