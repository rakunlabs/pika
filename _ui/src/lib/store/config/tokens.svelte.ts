// API-token slice of the config store. Re-exported via configStore in
// config.svelte.ts.

import type {
  CreateTokenRequest,
  CreateTokenResponse,
  PatchTokenRequest,
  TokenInfo,
} from '@/lib/types/config';
import { addToast } from '@/lib/store/toast.svelte';
import axios from 'axios';

export function createTokenStore() {
  let tokens = $state<TokenInfo[]>([]);

  // Token operations
  async function loadTokens(): Promise<void> {
    try {
      const response = await axios.get<TokenInfo[]>('/api/v1/tokens');
      tokens = response.data || [];
    } catch {
      tokens = [];
    }
  }

  async function createToken(req: CreateTokenRequest): Promise<CreateTokenResponse> {
    const response = await axios.post<CreateTokenResponse>('/api/v1/tokens', req);
    await loadTokens();
    return response.data;
  }

  async function deleteToken(id: string): Promise<void> {
    await axios.delete(`/api/v1/tokens/${id}`);
    await loadTokens();
    addToast('Token deleted', 'success');
  }

  async function patchToken(id: string, req: PatchTokenRequest): Promise<void> {
    await axios.patch(`/api/v1/tokens/${id}`, req);
    await loadTokens();
    addToast('Token updated', 'success');
  }

  return {
    get tokens() { return tokens; },
    loadTokens,
    createToken,
    deleteToken,
    patchToken,
  };
}
