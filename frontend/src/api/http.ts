// One transport, and one shape of failure.
//
// The API answers a refusal with a machine-readable code and, often, a detail
// body the UI needs — the positioned validation items on a claim submit, the
// demotions a withdrawal would cause. Those arrive as `detail`, which is the
// whole response body, and a caller branches on `code` rather than on prose.

import { API_BASE_URL, endpoints } from "./endpoints";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly detail: Record<string, unknown>;

  constructor(status: number, code: string, message: string, detail: Record<string, unknown>) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.detail = detail;
  }
}

export interface Tokens {
  access: string;
  refresh: string;
}

type TokenReader = () => Tokens | null;
type TokenWriter = (tokens: Tokens | null) => void;

export class Client {
  private readonly read: TokenReader;
  private readonly write: TokenWriter;
  private refreshing: Promise<boolean> | null = null;

  constructor(read: TokenReader, write: TokenWriter) {
    this.read = read;
    this.write = write;
  }

  get<T>(path: string, signal?: AbortSignal): Promise<T> {
    return this.send<T>("GET", path, undefined, signal);
  }

  post<T>(path: string, body?: unknown): Promise<T> {
    return this.send<T>("POST", path, body);
  }

  put<T>(path: string, body?: unknown): Promise<T> {
    return this.send<T>("PUT", path, body);
  }

  patch<T>(path: string, body?: unknown): Promise<T> {
    return this.send<T>("PATCH", path, body);
  }

  delete<T>(path: string): Promise<T> {
    return this.send<T>("DELETE", path);
  }

  /**
   * A 401 is retried once behind a refresh, and only once.
   *
   * The single in-flight promise matters: a screen that fires four reads on
   * mount would otherwise rotate the refresh token four times in parallel and
   * invalidate its own session.
   */
  private async send<T>(
    method: string,
    path: string,
    body?: unknown,
    signal?: AbortSignal,
  ): Promise<T> {
    const response = await this.raw(method, path, body, signal);
    if (response.status !== 401 || path.startsWith("/auth/")) {
      return this.decode<T>(response);
    }

    if (await this.refresh()) {
      return this.decode<T>(await this.raw(method, path, body, signal));
    }
    return this.decode<T>(response);
  }

  private raw(
    method: string,
    path: string,
    body?: unknown,
    signal?: AbortSignal,
  ): Promise<Response> {
    const tokens = this.read();
    const headers: Record<string, string> = { accept: "application/json" };
    if (body !== undefined) headers["content-type"] = "application/json";
    if (tokens) headers["authorization"] = `Bearer ${tokens.access}`;

    return fetch(`${API_BASE_URL}${path}`, {
      method,
      headers,
      credentials: "include",
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      ...(signal ? { signal } : {}),
    });
  }

  private refresh(): Promise<boolean> {
    if (this.refreshing) return this.refreshing;

    const tokens = this.read();
    if (!tokens) return Promise.resolve(false);

    this.refreshing = (async () => {
      try {
        const response = await this.raw("POST", endpoints.auth.refresh(), {
          refresh_token: tokens.refresh,
        });
        if (!response.ok) {
          this.write(null);
          return false;
        }
        const body = (await response.json()) as { access_token?: string; refresh_token?: string };
        if (!body.access_token || !body.refresh_token) {
          this.write(null);
          return false;
        }
        this.write({ access: body.access_token, refresh: body.refresh_token });
        return true;
      } catch {
        return false;
      } finally {
        this.refreshing = null;
      }
    })();

    return this.refreshing;
  }

  private async decode<T>(response: Response): Promise<T> {
    if (response.status === 204) return undefined as T;

    const text = await response.text();
    const body = parse(text);

    if (!response.ok) {
      const code = typeof body.error === "string" ? body.error : `http_${response.status}`;
      const message = typeof body.message === "string" ? body.message : defaultMessage(code);
      throw new ApiError(response.status, code, message, body);
    }
    return body as T;
  }
}

function parse(text: string): Record<string, unknown> {
  if (!text) return {};
  try {
    const value = JSON.parse(text) as unknown;
    return typeof value === "object" && value !== null
      ? (value as Record<string, unknown>)
      : { value };
  } catch {
    return { error: "malformed_response", message: text.slice(0, 200) };
  }
}

/**
 * Prose for the codes the API answers bare.
 *
 * Only these: where the server sends a message it is better than anything a
 * client could invent, because it knows which rule was broken.
 */
function defaultMessage(code: string): string {
  switch (code) {
    case "hiring_capability_required":
    case "verification_needed":
      return "This hiring account is not verified yet, so search and shortlisting stay closed.";
    case "forbidden":
      return "This account may not do that.";
    case "unauthenticated":
      return "Your session has expired. Sign in again.";
    case "claim_locked":
      return "A judged claim is frozen for seven days.";
    case "invalid_evidence":
      return "Some of this evidence could not be accepted.";
    case "not_found":
      return "That does not exist, or is not yours to see.";
    case "malformed_response":
      return "The server sent something this client could not read.";
    default:
      return "That did not work.";
  }
}
