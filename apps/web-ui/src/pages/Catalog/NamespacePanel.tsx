/**
 * Left-hand namespace tree for the Catalog. Selecting a node filters
 * the image table to that namespace and everything below it. Admins can
 * create namespaces, grant users access, and delete empty ones.
 */

import { useEffect, useMemo } from 'react';
import { ChevronDown, ChevronRight, Folder, FolderOpen, Layers, Plus, Shield, Trash2, X } from 'lucide-react';

import { useViewModel } from 'bizify';
import { currentUserViewModel } from '@/hooks/use-current-user';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { NamespacePanelViewModel } from './namespace-panel-vm';
import { buildTree, type TreeNode } from './namespace-tree';

interface Props {
  /** Selected namespace path; '' means all images. */
  selected: string;
  onSelect: (path: string) => void;
  /** Bumped by the parent after a refresh so counts stay current. */
  reloadKey?: number;
}

export function NamespacePanel({ selected, onSelect, reloadKey }: Props) {
  const vm = useViewModel(NamespacePanelViewModel);
  const s = vm.useSnapshot();
  const isAdmin = currentUserViewModel.isAdmin;
  const tree = useMemo(() => buildTree(s.items), [s.items]);

  // Keep the selected node visible (e.g. when arriving via a URL).
  useEffect(() => {
    vm.reveal(selected);
  }, [vm, selected]);

  useEffect(() => {
    if (reloadKey) void vm.refresh();
  }, [vm, reloadKey]);

  const renderNode = (node: TreeNode, depth: number) => {
    const open = !!s.expanded[node.path];
    const isSel = selected === node.path;
    const hasKids = node.children.length > 0;
    return (
      <div key={node.path}>
        <div
          className={`group flex items-center gap-1 rounded-md pr-1 text-sm ${
            isSel ? 'bg-accent text-accent-foreground' : 'hover:bg-accent/50'
          }`}
          style={{ paddingLeft: depth * 12 + 4 }}
        >
          <button
            type="button"
            className="h-6 w-5 shrink-0 text-muted-foreground"
            onClick={() => vm.toggle(node.path)}
            aria-label={open ? `Collapse ${node.path}` : `Expand ${node.path}`}
            disabled={!hasKids}
          >
            {hasKids ? open ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" /> : null}
          </button>
          <button
            type="button"
            className="flex min-w-0 flex-1 items-center gap-1.5 py-1 text-left"
            onClick={() => onSelect(node.path)}
            title={node.description ? `${node.path} — ${node.description}` : node.path}
          >
            {open && hasKids ? (
              <FolderOpen className="h-4 w-4 shrink-0 text-primary" />
            ) : (
              <Folder className="h-4 w-4 shrink-0 text-primary" />
            )}
            <span className="truncate">{node.name}</span>
            <span className="ml-auto pl-2 text-xs tabular-nums text-muted-foreground">{node.repoCount}</span>
          </button>
          {isAdmin && (
            <span className="hidden shrink-0 items-center group-hover:inline-flex">
              <IconBtn label={`New namespace under ${node.path}`} onClick={() => vm.openCreate(node.path)}>
                <Plus className="h-3.5 w-3.5" />
              </IconBtn>
              <IconBtn label={`Manage access to ${node.path}`} onClick={() => void vm.openGrants(node.path)}>
                <Shield className="h-3.5 w-3.5" />
              </IconBtn>
              {node.explicit && node.repoCount === 0 && !hasKids && (
                <IconBtn label={`Delete ${node.path}`} onClick={() => vm.askDelete(node.path)}>
                  <Trash2 className="h-3.5 w-3.5" />
                </IconBtn>
              )}
            </span>
          )}
        </div>
        {open && node.children.map((c) => renderNode(c, depth + 1))}
      </div>
    );
  };

  const available = s.grantableUsers.filter((u) => !s.grants.some((g) => g.user_id === u.id));

  return (
    <aside className="w-48 shrink-0 space-y-2" aria-label="Namespaces">
      <div className="flex items-center justify-between px-1">
        <h3 className="text-sm font-semibold">Namespaces</h3>
        {isAdmin && (
          <IconBtn label="New namespace" onClick={() => vm.openCreate()}>
            <Plus className="h-4 w-4" />
          </IconBtn>
        )}
      </div>

      <button
        type="button"
        onClick={() => onSelect('')}
        className={`flex w-full items-center gap-2 rounded-md px-2 py-1 text-sm ${
          selected === '' ? 'bg-accent text-accent-foreground' : 'hover:bg-accent/50'
        }`}
      >
        <Layers className="h-4 w-4 text-primary" />
        All images
      </button>

      {s.loading && <p className="px-2 text-xs text-muted-foreground">Loading…</p>}
      {s.error && <p className="px-2 text-xs text-destructive">{s.error}</p>}
      {!s.loading && !s.error && tree.length === 0 && (
        <p className="px-2 text-xs text-muted-foreground">
          No namespaces yet.{isAdmin ? ' Use + to create one.' : ''}
        </p>
      )}
      <div className="space-y-0.5">{tree.map((n) => renderNode(n, 0))}</div>

      {/* Create */}
      <Dialog open={s.createOpen} onOpenChange={(o) => !o && vm.closeCreate()}>
        <DialogContent>
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              const path = await vm.submitCreate();
              if (path) onSelect(path);
            }}
          >
            <DialogHeader>
              <DialogTitle>New namespace</DialogTitle>
              <DialogDescription>
                Nested paths are allowed, e.g. <code>org/team</code>. Missing parents are created too.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-4">
              <div className="space-y-2">
                <Label htmlFor="ns-path">Path</Label>
                <Input
                  id="ns-path"
                  value={s.createPath}
                  onChange={(e) => vm.setCreatePath(e.target.value)}
                  placeholder="org/team"
                  disabled={s.createSubmitting}
                  autoFocus
                  required
                />
                <p className="text-xs text-muted-foreground">Lowercase letters, digits, and . _ - in each segment.</p>
              </div>
              <div className="space-y-2">
                <Label htmlFor="ns-desc">Description (optional)</Label>
                <Input
                  id="ns-desc"
                  value={s.createDesc}
                  onChange={(e) => vm.setCreateDesc(e.target.value)}
                  disabled={s.createSubmitting}
                />
              </div>
              {s.createError && <p className="text-sm text-destructive">{s.createError}</p>}
            </div>
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => vm.closeCreate()} disabled={s.createSubmitting}>
                Cancel
              </Button>
              <Button type="submit" disabled={s.createSubmitting}>
                {s.createSubmitting ? 'Creating…' : 'Create'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Grants */}
      <Dialog open={s.grantsTarget !== null} onOpenChange={(o) => !o && vm.closeGrants()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Access to {s.grantsTarget}</DialogTitle>
            <DialogDescription>
              Granted users get their role&apos;s actions on every repository under this namespace, at any depth.
              A user with any grant can only reach granted repositories. Admins always have access.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 py-2">
            {s.grantsLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
            {!s.grantsLoading && s.grants.length === 0 && (
              <p className="text-sm text-muted-foreground">No users granted on this namespace.</p>
            )}
            <ul className="space-y-1">
              {s.grants.map((g) => (
                <li key={g.permission_id} className="flex items-center justify-between rounded-md border px-3 py-1.5 text-sm">
                  <span>
                    {g.username} <span className="text-xs text-muted-foreground">({g.role})</span>
                  </span>
                  <IconBtn label={`Revoke ${g.username}`} onClick={() => void vm.removeGrant(g.user_id)} disabled={s.grantsBusy}>
                    <X className="h-4 w-4" />
                  </IconBtn>
                </li>
              ))}
            </ul>
            <div className="flex items-center gap-2">
              <Select value={s.grantUserId} onValueChange={(v) => vm.setGrantUser(v)} disabled={s.grantsBusy}>
                <SelectTrigger className="flex-1">
                  <SelectValue placeholder={available.length ? 'Select a user…' : 'No more users to add'} />
                </SelectTrigger>
                <SelectContent>
                  {available.map((u) => (
                    <SelectItem key={u.id} value={String(u.id)}>
                      {u.username} ({u.role})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button onClick={() => void vm.addGrant()} disabled={!s.grantUserId || s.grantsBusy}>
                Grant
              </Button>
            </div>
            {s.grantsError && <p className="text-sm text-destructive">{s.grantsError}</p>}
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={() => vm.closeGrants()}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete */}
      <AlertDialog open={s.deleteTarget !== null} onOpenChange={(o) => !o && vm.cancelDelete()}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete namespace {s.deleteTarget}?</AlertDialogTitle>
            <AlertDialogDescription>
              Only empty namespaces can be deleted. Access grants on it are removed.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {s.deleteError && <p className="text-sm text-destructive">{s.deleteError}</p>}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={s.deleteSubmitting}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={s.deleteSubmitting}
              onClick={async (e) => {
                e.preventDefault();
                const removed = await vm.confirmDelete();
                if (removed && (selected === removed || selected.startsWith(removed + '/'))) onSelect('');
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </aside>
  );
}

function IconBtn({
  label,
  onClick,
  disabled,
  children,
}: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      className="rounded p-1 text-muted-foreground hover:text-foreground disabled:opacity-50"
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      title={label}
    >
      {children}
    </button>
  );
}
