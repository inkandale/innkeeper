import createClient, { type Middleware } from "openapi-fetch";
import type { paths } from "./schema";

/** loginPath is the login page URL that returns to path and search. */
export function loginPath(path: string, search: string): string {
  if (path === "/login") return path + search;
  return "/login?next=" + encodeURIComponent(path + search);
}

/** safeNext returns next if it's a path on this site, or "/". */
export function safeNext(next: string | null): string {
  if (!next) return "/";
  // Parse, as the browser will: it strips tabs and newlines, so prefix checks miss "/\t/evil".
  try {
    const u = new URL(next, window.location.origin);
    return u.origin === window.location.origin ? u.pathname + u.search + u.hash : "/";
  } catch {
    return "/"; // Unparseable, like "http://[".
  }
}

/** nextKey is the sessionStorage key where a redirect login saves where to land. */
export const nextKey = "innkeeper.next";

// Any 401 means the session is gone, so go log in and come back. A failed
// login is also a 401, so login calls are left to the login page.
const toLogin: Middleware = {
  onResponse({ request, response }) {
    const path = new URL(request.url).pathname;
    const loginCall = path.startsWith("/api/auth/") && path !== "/api/auth/me";
    if (response.status === 401 && !loginCall && window.location.pathname !== "/login") {
      window.location.assign(loginPath(window.location.pathname, window.location.search));
    }
  },
};

// fetch is looked up per call, not bound once, so tests can stub it. The
// base URL is absolute because Request rejects relative URLs outside a browser.
export const api = createClient<paths>({
  baseUrl: new URL("/api", window.location.origin).href,
  fetch: (req) => globalThis.fetch(req),
});
api.use(toLogin);

/** ApiError carries the API's problem detail. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    detail: string,
  ) {
    super(detail);
  }
}

/** must returns a response's data, or throws its error as an ApiError. */
export function must<T>(res: { data?: T; error?: unknown; response: Response }): T {
  if (res.error !== undefined || res.data === undefined) {
    const detail = (res.error as { detail?: string } | undefined)?.detail ?? res.response.statusText;
    throw new ApiError(res.response.status, detail);
  }
  return res.data;
}
