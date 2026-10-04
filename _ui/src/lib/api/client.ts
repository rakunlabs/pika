import { isAxiosError } from 'axios';

/** Shape of the JSON error body returned by the Pika API. */
export interface ApiErrorBody {
  message?: string;
  error?: string;
  request_id?: string;
}

function asErrorBody(data: unknown): ApiErrorBody | null {
  if (data && typeof data === 'object' && !(typeof Blob !== 'undefined' && data instanceof Blob)) {
    return data as ApiErrorBody;
  }
  return null;
}

function withRequestId(message: string, status: number | undefined, body: ApiErrorBody | null): string {
  const requestId = body?.request_id;
  if (requestId && status !== undefined && status >= 500) {
    return `${message} (request id: ${requestId})`;
  }
  return message;
}

/**
 * Extract a user-facing message from an axios / Error rejection.
 *
 * Preference order: `response.data.message`, `response.data.error`,
 * `err.message`, then `fallback`. For 5xx responses the backend's
 * `request_id` is appended so users can quote it in bug reports.
 */
export function apiErrorMessage(err: unknown, fallback: string): string {
  if (isAxiosError(err)) {
    const body = asErrorBody(err.response?.data);
    const status = err.response?.status;
    const msg = body?.message || body?.error;
    if (msg) return withRequestId(msg, status, body);
    if (err.response) return withRequestId(fallback, status, body);
    return err.message || fallback;
  }
  if (err instanceof Error) return err.message || fallback;
  if (err && typeof err === 'object' && 'message' in err) {
    const m = (err as { message?: unknown }).message;
    if (typeof m === 'string' && m) return m;
  }
  return fallback;
}

/**
 * Like {@link apiErrorMessage} but never falls back to the raw
 * `err.message` (e.g. "Request failed with status code 500"). Used where
 * the previous code only looked at the server-provided message.
 */
export function apiServerMessage(err: unknown, fallback: string): string {
  if (isAxiosError(err)) {
    const body = asErrorBody(err.response?.data);
    const msg = body?.message || body?.error;
    return withRequestId(msg || fallback, err.response?.status, body);
  }
  return fallback;
}

/**
 * Server-provided message, then the HTTP status text, then `fallback`.
 */
export function apiStatusMessage(err: unknown, fallback: string): string {
  if (isAxiosError(err)) {
    const body = asErrorBody(err.response?.data);
    const msg = body?.message || body?.error || err.response?.statusText;
    return withRequestId(msg || fallback, err.response?.status, body);
  }
  return fallback;
}

/**
 * Like {@link apiStatusMessage}, but also understands error bodies
 * delivered as a Blob (requests made with `responseType: 'blob'`).
 */
export async function apiBlobErrorMessage(err: unknown, fallback: string): Promise<string> {
  if (isAxiosError(err) && typeof Blob !== 'undefined' && err.response?.data instanceof Blob) {
    try {
      const parsed = JSON.parse(await err.response.data.text()) as ApiErrorBody;
      const msg = parsed?.message || parsed?.error;
      if (msg) return withRequestId(msg, err.response.status, parsed);
    } catch {
      // Non-JSON body — fall through to status-derived message.
    }
    return err.response.statusText || fallback;
  }
  return apiStatusMessage(err, fallback);
}

/** Server-provided error code (`response.data.error`), if any. */
export function apiErrorCode(err: unknown): string | undefined {
  if (isAxiosError(err)) return asErrorBody(err.response?.data)?.error;
  return undefined;
}

/** HTTP status of an axios rejection, if any. */
export function apiErrorStatus(err: unknown): number | undefined {
  return isAxiosError(err) ? err.response?.status : undefined;
}
