import { AxiosError, AxiosHeaders, type AxiosResponse } from 'axios';
import { describe, expect, it } from 'vitest';
import {
  apiBlobErrorMessage,
  apiErrorCode,
  apiErrorMessage,
  apiErrorStatus,
  apiServerMessage,
  apiStatusMessage,
} from './client';

function axiosErr(status: number | null, data?: unknown, statusText = '', message = 'Request failed'): AxiosError {
  const config = { headers: new AxiosHeaders() };
  const response: AxiosResponse | undefined =
    status === null
      ? undefined
      : { status, statusText, data, headers: {}, config };
  return new AxiosError(message, 'ERR_BAD_RESPONSE', config, undefined, response);
}

describe('apiErrorMessage', () => {
  it('prefers response.data.message', () => {
    expect(apiErrorMessage(axiosErr(400, { message: 'bad input' }), 'fallback')).toBe('bad input');
  });

  it('falls back to response.data.error', () => {
    expect(apiErrorMessage(axiosErr(401, { error: 'invalid_session' }), 'fallback')).toBe('invalid_session');
  });

  it('appends request_id for 5xx', () => {
    const err = axiosErr(500, { message: 'internal error', request_id: 'abc123' });
    expect(apiErrorMessage(err, 'fallback')).toBe('internal error (request id: abc123)');
  });

  it('does not append request_id for 4xx', () => {
    const err = axiosErr(404, { message: 'not found', request_id: 'abc123' });
    expect(apiErrorMessage(err, 'fallback')).toBe('not found');
  });

  it('uses the fallback (with request id) when a 5xx body has no message', () => {
    expect(apiErrorMessage(axiosErr(502, { request_id: 'r1' }), 'Save failed')).toBe(
      'Save failed (request id: r1)',
    );
  });

  it('uses err.message for network errors without a response', () => {
    expect(apiErrorMessage(axiosErr(null, undefined, '', 'Network Error'), 'fallback')).toBe('Network Error');
  });

  it('handles plain Errors, message-bearing objects and junk', () => {
    expect(apiErrorMessage(new Error('boom'), 'fallback')).toBe('boom');
    expect(apiErrorMessage({ message: 'objmsg' }, 'fallback')).toBe('objmsg');
    expect(apiErrorMessage('string', 'fallback')).toBe('fallback');
    expect(apiErrorMessage(undefined, 'fallback')).toBe('fallback');
    expect(apiErrorMessage(new Error(''), 'fallback')).toBe('fallback');
  });

  it('ignores non-object bodies', () => {
    expect(apiErrorMessage(axiosErr(500, '<html>oops</html>'), 'Save failed')).toBe('Save failed');
  });
});

describe('apiServerMessage', () => {
  it('never surfaces the generic axios message', () => {
    expect(apiServerMessage(axiosErr(500, undefined), 'Save failed')).toBe('Save failed');
    expect(apiServerMessage(new Error('boom'), 'Save failed')).toBe('Save failed');
  });

  it('uses the server message and request id', () => {
    expect(apiServerMessage(axiosErr(503, { message: 'down', request_id: 'z' }), 'x')).toBe(
      'down (request id: z)',
    );
  });
});

describe('apiStatusMessage', () => {
  it('falls back to statusText then fallback', () => {
    expect(apiStatusMessage(axiosErr(418, undefined, "I'm a teapot"), 'fb')).toBe("I'm a teapot");
    expect(apiStatusMessage(axiosErr(418, undefined, ''), 'fb')).toBe('fb');
  });
});

describe('apiBlobErrorMessage', () => {
  it('decodes JSON error bodies delivered as Blob', async () => {
    const blob = new Blob([JSON.stringify({ message: 'not exportable', request_id: 'q' })]);
    expect(await apiBlobErrorMessage(axiosErr(400, blob, 'Bad Request'), 'Export failed')).toBe(
      'not exportable',
    );
    const blob5xx = new Blob([JSON.stringify({ message: 'boom', request_id: 'q' })]);
    expect(await apiBlobErrorMessage(axiosErr(500, blob5xx), 'Export failed')).toBe('boom (request id: q)');
  });

  it('falls back to statusText for non-JSON blobs', async () => {
    const blob = new Blob(['not json']);
    expect(await apiBlobErrorMessage(axiosErr(502, blob, 'Bad Gateway'), 'Export failed')).toBe('Bad Gateway');
  });
});

describe('apiErrorCode / apiErrorStatus', () => {
  it('extracts code and status', () => {
    const err = axiosErr(409, { error: 'user_exists' });
    expect(apiErrorCode(err)).toBe('user_exists');
    expect(apiErrorStatus(err)).toBe(409);
    expect(apiErrorCode(new Error('x'))).toBeUndefined();
    expect(apiErrorStatus(new Error('x'))).toBeUndefined();
  });
});
