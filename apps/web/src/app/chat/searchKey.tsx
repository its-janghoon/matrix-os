'use client';

import { useCallback, useState, useSyncExternalStore } from 'react';

import {
  forgetSearchCredential,
  guessProvider,
  rememberSearchCredential,
  storedSearchCredential,
  type SearchCredential,
  type SearchProvider,
} from '@/lib/wallet/webSearch';

/**
 * Where the reader puts their own search key.
 *
 * THEIR key, not the deployment's. Search used to run through a route in this app
 * holding one key for everybody, which made it the only centralised dependency in a
 * page whose whole design is self-custody: one shared quota anyone could drain, and no
 * search at all unless that deployment was alive and funded. A key here is spent by the
 * reader who pasted it and by nobody else.
 *
 * Only providers that can actually be called from a page are offered. That is not a
 * preference - the first one tried, Brave, answers a CORS preflight with 405 and no
 * headers, so a browser cannot reach it whatever the key is.
 *
 * The key is held in localStorage, which any script on this origin can read. Said
 * plainly in the UI rather than left implied, and it is not a new exposure: this origin
 * already holds the delegate key that SPENDS the reader's budget, which is worth more
 * than a search quota.
 *
 * WHY useSyncExternalStore AND NOT AN EFFECT. Reading storage in an effect and calling
 * setState is a second copy of a value that already exists, and the two can disagree -
 * which is exactly the bug that made the budget's Close button vanish until a manual
 * refresh. eslint's `react-hooks/set-state-in-effect` refuses it, and it is right. The
 * stored credential is EXTERNAL state, so it is subscribed to and read on every render;
 * the only real state here is the reader's in-progress edit, which is not a copy of
 * anything.
 */
const FIELD =
  'w-full rounded border border-gray-700 bg-gray-900 px-3 py-2 font-mono text-sm text-gray-100';

/**
 * Storage has no change event of its own for same-tab writes, so the store notifies its
 * own subscribers after a write. `storage` covers the other-tab case, which is worth
 * having: a reader who clears the key in a second tab should not have this one keep
 * offering the tool.
 */
const listeners = new Set<() => void>();

function notify(): void {
  for (const l of listeners) l();
}

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange);
  window.addEventListener('storage', onChange);
  return () => {
    listeners.delete(onChange);
    window.removeEventListener('storage', onChange);
  };
}

/**
 * The snapshot must be REFERENTIALLY STABLE between real changes or React re-renders
 * for ever, and `storedSearchCredential` builds a fresh object each call. So the parsed
 * value is cached against the raw string it came from.
 */
let cachedRaw: string | null = null;
let cachedValue: SearchCredential | null = null;

function getSnapshot(): SearchCredential | null {
  let raw: string | null;
  try {
    raw = window.localStorage.getItem('matrix-search-key');
  } catch {
    raw = null;
  }
  if (raw !== cachedRaw) {
    cachedRaw = raw;
    cachedValue = storedSearchCredential();
  }
  return cachedValue;
}

/** No storage on the server, so the first paint shows the field empty. */
function getServerSnapshot(): SearchCredential | null {
  return null;
}

export function SearchKeyField() {
  const stored = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
  // The in-progress edit. Null means "not editing", and the field then shows whatever is
  // stored - so this is not a copy of storage, it is the thing storage does not know.
  const [draft, setDraft] = useState<SearchCredential | null>(null);

  const shown: SearchCredential = draft ?? stored ?? { provider: 'serper', key: '' };
  const isSaved = draft === null && stored !== null;

  const write = useCallback((next: SearchCredential | null) => {
    if (next === null) forgetSearchCredential();
    else rememberSearchCredential(next);
    setDraft(null);
    notify();
  }, []);

  return (
    <div className='mt-4'>
      <label className='mb-1 block text-sm text-gray-400' htmlFor='search-key'>
        Web search key (optional)
      </label>
      <div className='flex gap-2'>
        <select
          className='rounded border border-gray-700 bg-gray-900 px-2 py-2 text-sm text-gray-100'
          value={shown.provider}
          onChange={(e) => setDraft({ ...shown, provider: e.target.value as SearchProvider })}
          aria-label='Search provider'
        >
          <option value='serper'>Serper</option>
          <option value='tavily'>Tavily</option>
        </select>
        <input
          id='search-key'
          className={FIELD}
          type='password'
          value={shown.key}
          placeholder='paste a key to let the model search'
          onChange={(e) => {
            const key = e.target.value;
            // A guess at the provider, and only from the key's own shape. It would be
            // worse to send a key to the wrong company than to ask, so an unrecognised
            // shape leaves whatever is selected alone.
            const guessed = guessProvider(key);
            setDraft({ provider: guessed ?? shown.provider, key });
          }}
          spellCheck={false}
        />
        <button
          className='rounded border border-gray-700 px-3 py-2 text-sm text-gray-300 hover:text-gray-100'
          onClick={() => write(shown.key.trim() === '' ? null : shown)}
        >
          {shown.key.trim() === '' ? 'clear' : 'save'}
        </button>
      </div>
      <p className='mt-1 text-xs text-gray-500'>
        {isSaved
          ? 'Saved in this browser. The model can now search, and your search provider bills you ' +
            'for each one. Emptying the field and pressing clear removes it.'
          : 'Without one the model cannot search and will say so. The key stays in this browser — ' +
            'it never reaches a node or a seller — and any script on this page can read it, the ' +
            'same as the key that spends your budget.'}
      </p>
    </div>
  );
}
