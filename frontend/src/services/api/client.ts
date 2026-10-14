import { z } from 'zod';
import { ApiError, apiErrorBodySchema } from '@/types/api';
import { getApiUrl } from '@/lib/config';

const DEFAULT_TIMEOUT_MS = 10_000;

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE';
  body?: unknown;
  timeoutMs?: number;
  signal?: AbortSignal;
}

/**
 * Typed REST client for the Go backend. Normalizes every failure to the
 * `{ error: { code, message, request_id } }` shape and passes backend
 * request IDs through on `ApiError.requestId`.
 */
export const apiClient = {
  async request<T>(path: string, schema: z.ZodType<T>, opts: RequestOptions = {}): Promise<T> {
    const { method = 'GET', body, timeoutMs = DEFAULT_TIMEOUT_MS, signal } = opts;
    const url = `${getApiUrl()}${path}`;

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeoutMs);
    // Abort on the caller's signal too.
    signal?.addEventListener('abort', () => controller.abort(), { once: true });

    let res: Response;
    try {
      res = await fetch(url, {
        method,
        signal: controller.signal,
        headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
        body: body !== undefined ? JSON.stringify(body) : undefined,
      });
    } catch (err) {
      const aborted = err instanceof DOMException && err.name === 'AbortError';
      throw new ApiError(
        aborted ? 'timeout' : 'network_error',
        aborted ? `Request timed out after ${timeoutMs}ms` : 'Could not reach the API',
        'unavailable',
      );
    } finally {
      clearTimeout(timer);
    }

    const requestId = res.headers.get('x-request-id') ?? 'unavailable';
    const text = await res.text();
    let json: unknown = null;
    if (text) {
      try {
        json = JSON.parse(text);
      } catch {
        throw new ApiError('bad_response', 'API returned non-JSON response', requestId, res.status);
      }
    }

    if (!res.ok) {
      const parsed = apiErrorBodySchema.safeParse(json);
      if (parsed.success) {
        const e = parsed.data.error;
        throw new ApiError(e.code, e.message, e.request_id, res.status);
      }
      throw new ApiError('http_error', `API responded with HTTP ${res.status}`, requestId, res.status);
    }

    const parsed = schema.safeParse(json);
    if (!parsed.success) {
      throw new ApiError('bad_response', 'API response did not match the expected schema', requestId);
    }
    return parsed.data;
  },

  get<T>(path: string, schema: z.ZodType<T>, opts?: RequestOptions): Promise<T> {
    return this.request(path, schema, opts);
  },

  post<T>(path: string, body: unknown, schema: z.ZodType<T>, opts?: RequestOptions): Promise<T> {
    return this.request(path, schema, { ...opts, method: 'POST', body });
  },
};
