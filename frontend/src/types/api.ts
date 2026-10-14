import { z } from 'zod';

/** Mirrors the backend's error envelope: `{ "error": { code, message, request_id } }`. */
export const apiErrorBodySchema = z.object({
  error: z.object({
    code: z.string(),
    message: z.string(),
    request_id: z.string(),
  }),
});
export type ApiErrorBody = z.infer<typeof apiErrorBodySchema>;

/** Normalized error thrown by the service layer. */
export class ApiError extends Error {
  readonly code: string;
  readonly requestId: string;
  readonly status?: number;

  constructor(code: string, message: string, requestId = 'local', status?: number) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.requestId = requestId;
    this.status = status;
  }
}

/** `GET /api/health` response. */
export const healthSchema = z.object({
  status: z.string(),
  service: z.string(),
  database: z.string(),
});
export type HealthStatus = z.infer<typeof healthSchema>;
