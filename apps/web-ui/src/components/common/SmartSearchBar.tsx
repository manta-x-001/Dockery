/**
 * SmartSearchBar — one search box for everything. Free text filters image
 * names; typing `git.` offers GitHub-style qualifiers (git.repo:, git.branch:
 * …) in a dropdown, and a complete `git.x:value` token searches the git
 * label index instead.
 */

import { useRef, useState } from 'react';
import { GitBranch, Search, X } from 'lucide-react';

import { Input } from '@/components/ui/input';
import { Button } from '@/components/ui/button';
import { GIT_FIELDS, parseGitQuery, hasGitFilter, type GitFilters } from '@/lib/git-query';

interface Props {
  value: string;
  onChange: (value: string) => void;
  /** Called on Enter when the query contains at least one git.* filter. */
  onGitSearch: (filters: GitFilters) => void;
  placeholder?: string;
  className?: string;
}

interface Suggestion {
  key: string;
  title: string;
  hint: string;
  /** Replacement for the token being typed; ends with ':' for a qualifier. */
  insert?: string;
  /** True when picking it runs the git search. */
  run?: boolean;
}

export function SmartSearchBar({
  value,
  onChange,
  onGitSearch,
  placeholder = 'Search images, or try git.repo: git.branch: …',
  className = '',
}: Props) {
  const [open, setOpen] = useState(false);
  // -1 = nothing highlighted, so Enter on plain text just keeps the live filter.
  const [active, setActive] = useState(-1);
  const inputRef = useRef<HTMLInputElement>(null);

  // The token under the cursor is approximated by the last word.
  const lastWord = value.slice(value.search(/\S*$/));
  const head = value.slice(0, value.length - lastWord.length);

  const suggestions = ((): Suggestion[] => {
    const colon = lastWord.indexOf(':');
    if (colon === -1) {
      // Typing the qualifier name itself: "", "g", "git.", "git.br" …
      if ('git.'.startsWith(lastWord) || lastWord.startsWith('git.')) {
        return GIT_FIELDS.filter((f) => f.key.startsWith(lastWord)).map((f) => ({
          key: f.key,
          title: `${f.key}:`,
          hint: `${f.label}, e.g. ${f.example}`,
          insert: `${f.key}:`,
        }));
      }
      // A plain keyword: offer it as a value for every qualifier.
      const isHex = /^[0-9a-f]{7,}$/i.test(lastWord);
      return GIT_FIELDS.filter((f) => isHex || (f.key !== 'git.commit' && f.key !== 'git.hash')).map((f) => ({
        key: f.key,
        title: `${f.key}:${lastWord}`,
        hint: f.label,
        insert: `${f.key}:${lastWord}`,
        run: true,
      }));
    }
    const field = GIT_FIELDS.find((f) => f.key === lastWord.slice(0, colon));
    const val = lastWord.slice(colon + 1);
    if (!field) return [];
    if (!val) return [{ key: field.key, title: `${field.key}:`, hint: `Type ${field.label.toLowerCase()}, e.g. ${field.example}` }];
    return [{ key: field.key, title: `${field.key}:${val}`, hint: `Find images by ${field.label.toLowerCase()}`, insert: lastWord, run: true }];
  })();

  const shown = open && suggestions.length > 0;

  const pick = (s: Suggestion) => {
    if (s.run && s.insert) {
      const next = head + s.insert;
      onChange(next);
      setOpen(false);
      onGitSearch(parseGitQuery(next).filters);
    } else if (s.insert) {
      onChange(head + s.insert);
      setActive(-1);
      inputRef.current?.focus();
    }
  };

  const submit = () => {
    const { filters } = parseGitQuery(value);
    if (hasGitFilter(filters)) {
      setOpen(false);
      onGitSearch(filters);
    }
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown' && shown) {
      e.preventDefault();
      setActive((a) => (a + 1) % suggestions.length);
    } else if (e.key === 'ArrowUp' && shown) {
      e.preventDefault();
      setActive((a) => (a <= 0 ? suggestions.length - 1 : a - 1));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const s = shown && active >= 0 ? suggestions[active] : undefined;
      if (s) pick(s);
      else submit();
    } else if (e.key === 'Tab' && shown) {
      e.preventDefault();
      pick(suggestions[Math.max(active, 0)]);
    } else if (e.key === 'Escape') {
      setOpen(false);
    }
  };

  return (
    <div className={`relative ${className}`}>
      <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-neutral-text" />
      <Input
        ref={inputRef}
        type="text"
        role="combobox"
        aria-expanded={shown}
        aria-autocomplete="list"
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          setActive(-1);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onKeyDown={onKeyDown}
        placeholder={placeholder}
        className="pl-10 pr-10"
      />
      {value && (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onChange('')}
          className="absolute right-1 top-1/2 -translate-y-1/2 h-7 w-7 p-0"
        >
          <X className="h-4 w-4" />
        </Button>
      )}
      {shown && (
        <ul
          role="listbox"
          className="absolute z-50 mt-1 w-full overflow-hidden rounded-md border bg-popover text-popover-foreground shadow-md"
        >
          {suggestions.map((s, i) => (
            <li
              key={s.key + s.title}
              role="option"
              aria-selected={i === active}
              // mousedown (not click) so the input's blur doesn't close the list first
              onMouseDown={(e) => {
                e.preventDefault();
                pick(s);
              }}
              onMouseEnter={() => setActive(i)}
              className={`flex cursor-pointer items-center gap-3 px-3 py-2 text-sm ${i === active ? 'bg-accent' : ''}`}
            >
              <GitBranch className="h-4 w-4 shrink-0 text-muted-foreground" />
              <span className="font-mono">{s.title}</span>
              <span className="truncate text-muted-foreground">{s.hint}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
