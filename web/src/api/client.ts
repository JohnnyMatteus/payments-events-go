import { ofetch, type FetchError } from 'ofetch';
import type { z } from 'zod';
import { setToken, token } from '@/auth/session';

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly code: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

const http = ofetch.create({
  baseURL: '/api/v1',
  retry: 0,
  onRequest({ options }) {
    if (token.value) options.headers.set('Authorization', `Bearer ${token.value}`);
  },
});

interface Problem {
  type?: string;
  detail?: string;
}

async function call<S extends z.ZodType>(schema: S | null, path: string, init: Parameters<typeof http>[1] = {}): Promise<z.infer<S>> {
  try {
    const body: unknown = await http(path, init);
    return schema ? schema.parse(body) : (undefined as z.infer<S>);
  } catch (error) {
    const failure = error as FetchError<Problem>;
    if (failure.status === 401 && token.value) setToken(null);
    if (failure.status) {
      const code = failure.data?.type?.split('/').pop() ?? 'error';
      throw new ApiError(failure.status, failure.data?.detail ?? `Falha ${String(failure.status)} em ${path}`, code);
    }
    throw error;
  }
}

export const get = <S extends z.ZodType>(schema: S, path: string, query?: Record<string, string | undefined>) =>
  call(schema, path, query ? { query: Object.fromEntries(Object.entries(query).filter(([, v]) => v)) } : {});

export const post = <S extends z.ZodType>(schema: S | null, path: string, body: Record<string, unknown>) =>
  call(schema, path, { method: 'POST', body });

export const putCsv = <S extends z.ZodType>(schema: S, path: string, csv: string) =>
  call(schema, path, { method: 'PUT', body: csv, headers: { 'Content-Type': 'text/csv' } });
