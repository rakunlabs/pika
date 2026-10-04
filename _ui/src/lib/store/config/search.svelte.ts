// Full-text search slice of the config store (SSE-backed). Re-exported
// via configStore in config.svelte.ts.

import type { SearchMode, SearchResult } from '@/lib/types/config';
import { addToast } from '@/lib/store/toast.svelte';
import { withBasePath } from '@/lib/basepath';

export function createSearchStore() {
  let searchQuery = $state('');
  let searchResults = $state<SearchResult[]>([]);
  let isSearching = $state(false);
  // Session-only: remembered across searches but not persisted to
  // storage. Default 'all' keeps the existing behaviour for users who
  // never touch the toggle.
  let searchMode = $state<SearchMode>('all');

  // Search operations
  let searchAbortController: AbortController | null = null;

  function search(query: string, mode?: SearchMode): void {
    // Cancel any ongoing search
    cancelSearch();

    searchQuery = query;
    if (mode) searchMode = mode;

    if (!query.trim()) {
      searchResults = [];
      isSearching = false;
      return;
    }

    isSearching = true;
    searchResults = [];

    const controller = new AbortController();
    searchAbortController = controller;

    // Only attach mode when 'name' — omitting it keeps URLs short and
    // makes the server-side default ('all') the source of truth.
    const params = new URLSearchParams({ q: query.trim() });
    if (searchMode === 'name') params.set('mode', 'name');

    const eventSource = new EventSource(withBasePath(`/api/v1/search?${params.toString()}`));

    // Handle abort — close the connection
    controller.signal.addEventListener('abort', () => {
      eventSource.close();
      isSearching = false;
    });

    eventSource.onmessage = (event) => {
      if (controller.signal.aborted) return;

      try {
        const result: SearchResult = JSON.parse(event.data);
        searchResults = [...searchResults, result];
      } catch {
        // skip bad data
      }
    };

    eventSource.addEventListener('done', () => {
      eventSource.close();
      isSearching = false;
      searchAbortController = null;
    });

    // Server-side failure: the backend emits `event: error` with
    // {"message": "..."} before `done`. Native connection errors also
    // dispatch an `error` Event (without `data`) — those are handled
    // by onerror below, so only react to MessageEvents here.
    eventSource.addEventListener('error', (event) => {
      if (!(event instanceof MessageEvent) || controller.signal.aborted) return;
      let msg = 'Search failed';
      try {
        const body = JSON.parse(event.data) as { message?: string };
        if (body?.message) msg = body.message;
      } catch {
        // keep generic message
      }
      eventSource.close();
      isSearching = false;
      searchAbortController = null;
      addToast(msg, 'alert');
    });

    eventSource.onerror = () => {
      eventSource.close();
      isSearching = false;
      searchAbortController = null;
    };
  }

  function cancelSearch(): void {
    if (searchAbortController) {
      searchAbortController.abort();
      searchAbortController = null;
    }
    isSearching = false;
  }

  function clearSearch(): void {
    cancelSearch();
    searchQuery = '';
    searchResults = [];
  }

  // setSearchMode updates the mode and, if a query is already active,
  // re-runs the search immediately so the user sees the effect of the
  // toggle without retyping. No-op if the mode didn't actually change.
  function setSearchMode(mode: SearchMode): void {
    if (searchMode === mode) return;
    searchMode = mode;
    if (searchQuery.trim()) {
      search(searchQuery, mode);
    }
  }

  return {
    get searchQuery() { return searchQuery; },
    get searchResults() { return searchResults; },
    get isSearching() { return isSearching; },
    get searchMode() { return searchMode; },
    search,
    cancelSearch,
    clearSearch,
    setSearchMode,
  };
}
