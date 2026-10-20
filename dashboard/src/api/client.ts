/**
 * Fetch wrapper for the Kyurem management API.
 *
 * Responsibilities:
 *  - resolve the base URL (same-origin `/api` by default),
 *  - attach the bearer token when one is configured,
 *  - turn non-2xx responses into an `ApiError` carrying the API's own
 *    `error.message` and the HTTP status code.
 */

/** localStorage key holding the token typed into the Settings page. */
export const API_TOKEN_STORAGE_KEY = "kyurem.apiToken";

export interface ApiErrorPayload {
  error?: {
    code?: string;
    message?: string;
  };
}

/** Error thrown for any non-2xx response of the management API. */
export class ApiError extends Error {
  /** HTTP status code of the failing response. */
  readonly status: number;
  /** Machine readable error code reported by the gateway, when present. */
  readonly code: string;

  constructor(status: number, message: string, code = "http_error") {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export type QueryParams = Record<string, string | number | boolean | undefined | null>;

/** Base URL of the management API without a trailing slash. */
export function getApiBaseUrl(): string {
  const configured = import.meta.env.VITE_KYUREM_API_BASE;
  const base = typeof configured === "string" && configured.trim() !== "" ? configured.trim() : "/api";
  return base.replace(/\/+$/, "");
}

/** Token stored by the Settings page, or an empty string. */
export function getStoredToken(): string {
  try {
    return window.localStorage.getItem(API_TOKEN_STORAGE_KEY) ?? "";
  } catch {
    return "";
  }
}

/** Persist (or clear) the token typed into the Settings page. */
export function setStoredToken(token: string): void {
  try {
    const trimmed = token.trim();
    if (trimmed === "") {
      window.localStorage.removeItem(API_TOKEN_STORAGE_KEY);
    } else {
      window.localStorage.setItem(API_TOKEN_STORAGE_KEY, trimmed);
    }
  } catch {
    // Storage can be unavailable (private mode, disabled cookies); the token
    // then simply lives for the lifetime of the page.
  }
}

/**
 * Effective token: a runtime token from the Settings page wins over the
 * build-time `VITE_KYUREM_API_TOKEN`.
 */
export function getApiToken(): string {
  const stored = getStoredToken();
  if (stored !== "") return stored;
  const configured = import.meta.env.VITE_KYUREM_API_TOKEN;
  return typeof configured === "string" ? configured.trim() : "";
}

/**
 * Build a full request URL from a path and optional query parameters.
 *
 * The base URL is the prefix under which the management API is reachable
 * (`/api` by default, the same origin the compose stack proxies). Contract
 * paths already start with that prefix, so it is not applied twice; an
 * absolute base (`http://host:8082`) simply provides the origin.
 */
export function buildUrl(path: string, query?: QueryParams): string {
  const base = getApiBaseUrl();
  const suffix = path.startsWith("/") ? path : `/${path}`;

  let origin = "";
  let prefix = base;
  if (/^https?:\/\//i.test(base)) {
    try {
      const parsed = new URL(base);
      origin = parsed.origin;
      prefix = parsed.pathname.replace(/\/+$/, "");
    } catch {
      origin = "";
      prefix = base;
    }
  } else {
    prefix = base.replace(/\/+$/, "");
  }

  const alreadyPrefixed =
    prefix !== "" && (suffix === prefix || suffix.startsWith(`${prefix}/`));
  const joined = alreadyPrefixed ? suffix : `${prefix}${suffix}`;
  const url = `${origin}${joined}`;

  if (!query) return url;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === "") continue;
    params.set(key, String(value));
  }
  const queryString = params.toString();
  return queryString === "" ? url : `${url}?${queryString}`;
}

async function toApiError(response: Response): Promise<ApiError> {
  let message = "";
  let code = "http_error";
  try {
    const body = (await response.json()) as ApiErrorPayload;
    if (body && typeof body === "object" && body.error) {
      if (typeof body.error.message === "string" && body.error.message !== "") {
        message = body.error.message;
      }
      if (typeof body.error.code === "string" && body.error.code !== "") {
        code = body.error.code;
      }
    }
  } catch {
    // The gateway did not answer with the documented JSON error shape.
  }
  const fallback = response.statusText && response.statusText !== "" ? response.statusText : "Request failed";
  return new ApiError(response.status, message !== "" ? message : fallback, code);
}

export interface ApiRequestOptions {
  query?: QueryParams;
  method?: string;
  signal?: AbortSignal;
}

/**
 * Perform a JSON request against the management API.
 *
 * Resolves with the parsed body, or throws an {@link ApiError} whose message
 * is the gateway's own `error.message` and whose `status` is the HTTP code.
 */
export async function apiFetch<T>(path: string, options: ApiRequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const token = getApiToken();
  if (token !== "") {
    headers.Authorization = `Bearer ${token}`;
  }

  const response = await fetch(buildUrl(path, options.query), {
    method: options.method ?? "GET",
    headers,
    signal: options.signal,
  });

  if (!response.ok) {
    throw await toApiError(response);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  try {
    return (await response.json()) as T;
  } catch {
    throw new ApiError(response.status, "Gateway returned a malformed response");
  }
}
