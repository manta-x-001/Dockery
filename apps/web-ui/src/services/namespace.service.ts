import { api } from './api';

export interface NamespaceView {
  path: string;
  description?: string;
  /** Created by an admin; false when it exists only because repos live under it. */
  explicit: boolean;
  /** Visible repositories at any depth below. */
  repo_count: number;
}

export interface NamespaceGrantView {
  permission_id: number;
  user_id: number;
  username: string;
  role: string;
}

const q = (path: string) => `path=${encodeURIComponent(path)}`;

export const namespaceService = {
  list: () => api.get<{ items: NamespaceView[] }>('/api/namespaces'),

  create: (req: { path: string; description?: string }) =>
    api.post<NamespaceView>('/api/namespaces', { description: '', ...req }),

  remove: (path: string) => api.delete<null>(`/api/namespaces?${q(path)}`),

  listGrants: (path: string) =>
    api.get<{ items: NamespaceGrantView[] }>(`/api/namespaces/grants?${q(path)}`),

  grant: (path: string, userId: number) =>
    api.post<null>('/api/namespaces/grants', { path, user_id: userId }),

  revoke: (path: string, userId: number) =>
    api.delete<null>(`/api/namespaces/grants?${q(path)}&user_id=${userId}`),
};
