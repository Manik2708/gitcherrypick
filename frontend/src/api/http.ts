// One transport, and one shape of failure.
//
// The API answers a refusal with a machine-readable code and, often, a detail
// body the UI needs — the positioned validation items on a claim submit, the
// demotions a withdrawal would cause. Those arrive as `detail`, which is the
// whole response body, and a caller branches on `code` rather than on prose.

import type { BlockingRole } from "../contract";
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

  /** The request key a refusal is about (ADR-0024 decisions 16–17). */
  get field(): string | undefined {
    return typeof this.detail.field === "string" ? this.detail.field : undefined;
  }

  /** 1-based, and only when `field` is a list. */
  get position(): number | undefined {
    return typeof this.detail.position === "number" ? this.detail.position : undefined;
  }

  /** The draft or open roles `address_in_use` and `onsite_roles_exist` name. */
  get roles(): BlockingRole[] {
    const roles = this.detail.roles;
    if (!Array.isArray(roles)) return [];
    return roles.filter(isBlockingRole);
  }
}

function isBlockingRole(value: unknown): value is BlockingRole {
  if (typeof value !== "object" || value === null) return false;
  const role = value as Record<string, unknown>;
  return (
    typeof role.id === "string" &&
    typeof role.title === "string" &&
    (role.status === "draft" || role.status === "open")
  );
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
      const message =
        typeof body.message === "string"
          ? body.message
          : defaultMessage(code, typeof body.field === "string" ? body.field : undefined);
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

/** What an `invalid_role` naming each field means, in the form's words. */
const ROLE_FIELDS: Record<string, string> = {
  title: "A role needs a title.",
  engagement: "Pick a kind of work.",
  currency: "Pick a currency for the amount.",
  yearly_ctc: "The total package must be more than zero.",
  yearly_base: "The base must be more than zero and no more than the total package.",
  hourly_rate: "The hourly rate must be more than zero.",
  expected_hours: "Hours a week must be between 1 and 168.",
  min_office_yoe: "A minimum must be between 0 and 80 years.",
  min_oss_yoe: "A minimum must be between 0 and 80 years.",
  max_interview_rounds: "Interview rounds must be at least 1, or blank.",
  avg_days_to_offer: "Days to an offer cannot be negative.",
  questions: "A screening question cannot be empty.",
  reason: "Pick a reason for closing.",
  note: "Say what happened.",
  hired: "Pick who you hired from the people who accepted.",
  role_create_authority: "Pick one of the settings offered.",
  role_update_authority: "Pick one of the settings offered.",
  role_close_authority: "Pick one of the settings offered.",
  tentative_result_date: "Give a date in the future.",
};

/** The address body's fields, top-level on the address routes (amendment 1 §1.8). */
const ADDRESS_FIELDS: Record<string, string> = {
  country: "Pick a country.",
  city: "Give a city.",
  street1: "Give a street.",
};

const ONBOARDING_FIELDS: Record<string, string> = {
  name: "Give the company's name.",
  email: "Give a company email address.",
  headcount: "Pick a headcount.",
  workplace: "Say whether you have an office or are fully remote.",
  "address.country": "Give the office's country as a two-letter code.",
  "address.city": "Give the office's city.",
  "address.street1": "Give the office's street.",
  address: "A fully remote company gives no address.",
};

/**
 * Prose for the codes the API answers bare.
 *
 * Only these: where the server sends a message it is better than anything a
 * client could invent, because it knows which rule was broken. `field` picks
 * the sentence where one code covers several controls.
 */
export function defaultMessage(code: string, field?: string): string {
  switch (code) {
    case "invalid_role":
      if (field && ROLE_FIELDS[field]) return ROLE_FIELDS[field];
      return "This role could not be saved.";
    case "invalid_address":
      if (field && ADDRESS_FIELDS[field]) return ADDRESS_FIELDS[field];
      return "Check the address.";
    case "invalid_onboarding":
      if (field && ONBOARDING_FIELDS[field]) return ONBOARDING_FIELDS[field];
      return "Check the form.";
    case "role_location_required":
      return "Say where the work happens.";
    case "role_location_invalid":
      return "That location cannot be used.";
    case "onsite_needs_address":
      return "Pick an office for onsite work.";
    case "remote_anywhere_exclusive":
      return "Remote from anywhere cannot sit beside remote in a country.";
    case "address_not_found":
      return "That office is not one of your organisation's.";
    case "address_removed":
      return "That office has been removed.";
    case "address_in_use":
      return "Draft or open roles still use this office.";
    case "main_office_not_removable":
      return "The main office cannot be removed.";
    case "organization_fully_remote":
      return "Your organisation is fully remote.";
    case "office_request_pending":
      return "An office request is already waiting for review.";
    case "organization_has_office":
      return "Your organisation already has an office.";
    case "onsite_roles_exist":
      return "Draft or open roles are still onsite.";
    case "request_already_decided":
      return "That request has already been decided.";
    case "reason_required":
      return "A rejection must carry a reason.";
    case "invalid_id":
      return "That id is not well formed.";
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
    case "invalid_state":
      return "That sign-in link did not match the one this browser started. Begin again.";
    case "oauth_exchange_failed":
      return "GitHub would not exchange that sign-in code. It may already have been used.";
    case "email_not_verified":
      return "That GitHub account has no verified email address this platform may use.";
    case "no_hirer_account":
      return "That identity has no hiring account. Register one, or sign in as a contributor.";
    case "email_verification_required":
      return "That link is not one we issued. Ask your organisation to add you again.";
    case "verification_already_used":
      return "That link has already been used. Your seat exists — sign in with the username your organisation gave you.";
    case "verification_expired":
      return "That link has expired. Ask for another and it will arrive within the hour.";
    case "username_taken":
      return "That username is not available.";
    case "not_an_org_member":
      return "Only an owner of this organisation may do that.";
    case "malformed_response":
      return "The server sent something this client could not read.";
    default:
      return "That did not work.";
  }
}
