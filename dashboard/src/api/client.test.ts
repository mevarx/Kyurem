import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiFetch, ApiError, buildUrl, getStoredToken, setStoredToken } from "./client";

const TOKEN_KEY = "kyurem.apiToken";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("api client", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.stubGlobal("fetch", vi.fn());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    window.localStorage.clear();
  });

  it("sends the bearer token when one is configured", async () => {
    setStoredToken("s3cret");
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    await apiFetch<{ ok: boolean }>("/api/v1/settings");

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("/api/v1/settings");
    const headers = init.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer s3cret");
    expect(headers.Accept).toBe("application/json");
  });

  it("omits the authorization header when the token is empty", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ok: true }));
    vi.stubGlobal("fetch", fetchMock);

    await apiFetch<{ ok: boolean }>("/api/v1/settings");

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const headers = init.headers as Record<string, string>;
    expect(headers.Authorization).toBeUndefined();
    expect(headers.Accept).toBe("application/json");
  });

  it("surfaces the API error message on a 4xx response", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({ error: { code: "unauthorized", message: "missing bearer token" } }, 401),
      ),
    );

    await expect(apiFetch("/api/v1/tasks")).rejects.toThrow("missing bearer token");
  });

  it("keeps the HTTP status code on the thrown error", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({ error: { code: "not_found", message: "no such task" } }, 404),
      ),
    );

    const error = await apiFetch("/api/v1/tasks/nope").catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(404);
    expect((error as ApiError).message).toBe("no such task");
    expect((error as ApiError).code).toBe("not_found");
  });

  it("falls back to the status text when the body is not the documented shape", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("boom", { status: 500 })));

    const error = await apiFetch("/api/v1/overview").catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(500);
  });

  it("stores and clears the runtime token under the documented key", () => {
    setStoredToken("token-value");
    expect(window.localStorage.getItem(TOKEN_KEY)).toBe("token-value");
    expect(getStoredToken()).toBe("token-value");

    setStoredToken("  ");
    expect(window.localStorage.getItem(TOKEN_KEY)).toBeNull();
    expect(getStoredToken()).toBe("");
  });

  it("builds query strings and skips empty parameters", () => {
    expect(buildUrl("/api/v1/tasks", { status: "failed", agent: "", limit: 50 })).toBe(
      "/api/v1/tasks?status=failed&limit=50",
    );
  });
});
