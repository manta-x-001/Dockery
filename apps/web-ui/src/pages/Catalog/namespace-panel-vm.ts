/**
 * State for the Catalog's left-hand namespace tree: the node list,
 * expand/collapse, and the admin dialogs (create, grants, delete).
 */

import { ViewModelBase } from 'bizify';
import {
  namespaceService,
  type NamespaceGrantView,
  type NamespaceView,
} from '@/services/namespace.service';
import { userService, type UserView } from '@/services/user.service';
import { ApiError } from '@/services/api';
import { ancestorsOf } from './namespace-tree';

interface State {
  items: NamespaceView[];
  loading: boolean;
  error: string | null;
  /** path → expanded; absent means collapsed. */
  expanded: Record<string, boolean>;

  createOpen: boolean;
  createPath: string;
  createDesc: string;
  createSubmitting: boolean;
  createError: string | null;

  deleteTarget: string | null;
  deleteSubmitting: boolean;
  deleteError: string | null;

  grantsTarget: string | null;
  grants: NamespaceGrantView[];
  grantableUsers: UserView[];
  grantUserId: string;
  grantsLoading: boolean;
  grantsBusy: boolean;
  grantsError: string | null;
}

const errMsg = (err: unknown, fallback: string) => (err instanceof ApiError ? err.message : fallback);

export class NamespacePanelViewModel extends ViewModelBase<State> {
  protected $data(): State {
    return {
      items: [],
      loading: true,
      error: null,
      expanded: {},
      createOpen: false,
      createPath: '',
      createDesc: '',
      createSubmitting: false,
      createError: null,
      deleteTarget: null,
      deleteSubmitting: false,
      deleteError: null,
      grantsTarget: null,
      grants: [],
      grantableUsers: [],
      grantUserId: '',
      grantsLoading: false,
      grantsBusy: false,
      grantsError: null,
    };
  }

  protected onMount() {
    void this.refresh();
  }

  async refresh() {
    try {
      const { items } = await namespaceService.list();
      Object.assign(this.data, { items, loading: false, error: null });
    } catch (err) {
      Object.assign(this.data, { loading: false, error: errMsg(err, 'Failed to load namespaces') });
    }
  }

  toggle(path: string) {
    this.data.expanded = { ...this.data.expanded, [path]: !this.data.expanded[path] };
  }

  /** Expand every ancestor of `path` so a selected nested node is visible. */
  reveal(path: string) {
    if (!path) return;
    const next = { ...this.data.expanded };
    for (const a of ancestorsOf(path)) next[a] = true;
    this.data.expanded = next;
  }

  // --- create ---------------------------------------------------------

  openCreate(parent = '') {
    Object.assign(this.data, {
      createOpen: true,
      createPath: parent ? `${parent}/` : '',
      createDesc: '',
      createError: null,
    });
  }

  closeCreate() {
    this.data.createOpen = false;
  }

  setCreatePath(v: string) {
    this.data.createPath = v;
  }

  setCreateDesc(v: string) {
    this.data.createDesc = v;
  }

  /** Resolves to the created path, or null on failure (error is in state). */
  async submitCreate(): Promise<string | null> {
    const path = this.data.createPath.trim().replace(/^\/+|\/+$/g, '');
    if (!path) {
      this.data.createError = 'Path is required';
      return null;
    }
    Object.assign(this.data, { createSubmitting: true, createError: null });
    try {
      await namespaceService.create({ path, description: this.data.createDesc.trim() });
      this.reveal(path);
      this.data.createOpen = false;
      await this.refresh();
      return path;
    } catch (err) {
      this.data.createError = errMsg(err, 'Failed to create namespace');
      return null;
    } finally {
      this.data.createSubmitting = false;
    }
  }

  // --- delete ---------------------------------------------------------

  askDelete(path: string) {
    Object.assign(this.data, { deleteTarget: path, deleteError: null });
  }

  cancelDelete() {
    this.data.deleteTarget = null;
  }

  async confirmDelete(): Promise<string | null> {
    const path = this.data.deleteTarget;
    if (!path) return null;
    Object.assign(this.data, { deleteSubmitting: true, deleteError: null });
    try {
      await namespaceService.remove(path);
      this.data.deleteTarget = null;
      await this.refresh();
      return path;
    } catch (err) {
      this.data.deleteError = errMsg(err, 'Failed to delete namespace');
      return null;
    } finally {
      this.data.deleteSubmitting = false;
    }
  }

  // --- grants ---------------------------------------------------------

  async openGrants(path: string) {
    Object.assign(this.data, {
      grantsTarget: path,
      grants: [],
      grantUserId: '',
      grantsError: null,
      grantsLoading: true,
    });
    try {
      const [g, u] = await Promise.all([namespaceService.listGrants(path), userService.list()]);
      if (this.data.grantsTarget !== path) return;
      Object.assign(this.data, {
        grants: g.items,
        grantableUsers: u.items.filter((x) => x.role !== 'admin' && !x.disabled),
        grantsLoading: false,
      });
    } catch (err) {
      Object.assign(this.data, { grantsLoading: false, grantsError: errMsg(err, 'Failed to load grants') });
    }
  }

  closeGrants() {
    this.data.grantsTarget = null;
  }

  setGrantUser(id: string) {
    this.data.grantUserId = id;
  }

  async addGrant() {
    const path = this.data.grantsTarget;
    const uid = Number(this.data.grantUserId);
    if (!path || !uid) return;
    await this.mutateGrants(path, () => namespaceService.grant(path, uid), 'Failed to grant access');
    this.data.grantUserId = '';
  }

  async removeGrant(userId: number) {
    const path = this.data.grantsTarget;
    if (!path) return;
    await this.mutateGrants(path, () => namespaceService.revoke(path, userId), 'Failed to revoke access');
  }

  private async mutateGrants(path: string, op: () => Promise<unknown>, fallback: string) {
    Object.assign(this.data, { grantsBusy: true, grantsError: null });
    try {
      await op();
      const g = await namespaceService.listGrants(path);
      if (this.data.grantsTarget === path) this.data.grants = g.items;
    } catch (err) {
      this.data.grantsError = errMsg(err, fallback);
    } finally {
      this.data.grantsBusy = false;
    }
  }
}
