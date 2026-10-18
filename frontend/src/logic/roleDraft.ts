// What is wrong with a role, and which control it is about.
//
// The API names the field of every refusal now (ADR-0024 decision 16), so this
// no longer mirrors RoleService.validate. What is left is what the form can get
// into before a round trip — and the mapping from a refusal back to the control
// that caused it.

import type { ApiError } from "../api/http";
import type { LocationRequest, OrgRole, RoleAuthority } from "../contract";
import type { LocationChoice } from "./roleLocations";

/**
 * Which part of the location controls a problem belongs beside.
 *
 * `where` is the whole set (no entries at all); `remote` and `onsite` are the
 * two switches; `addressId` narrows an onsite problem to one office.
 */
export type LocationSlot = "where" | "remote" | "onsite";

export interface RoleProblem {
  /** The request key, as the server names it. */
  field: string;
  message: string;
  slot?: LocationSlot;
  addressId?: string;
}

/** The first thing worth fixing before sending, or null. */
export function draftProblem(title: string, choice: LocationChoice): RoleProblem | null {
  if (!title.trim()) return { field: "title", message: "A role needs a title." };

  if (!choice.remote && !choice.onsite) {
    return { field: "locations", slot: "where", message: "Say where the work happens." };
  }
  if (choice.remote && !choice.anywhere && choice.countries.length === 0) {
    return {
      field: "locations",
      slot: "remote",
      message: "Pick at least one country, or allow anywhere.",
    };
  }
  if (choice.onsite && choice.addressIds.length === 0) {
    return { field: "locations", slot: "onsite", message: "Pick an office for onsite work." };
  }
  return null;
}

/**
 * A refusal, placed beside its control.
 *
 * `sent` is the locations list of the request that was refused: a position is
 * read against it, not against whatever the form holds now. Null when the
 * refusal names no field — that one belongs under the form, not beside a box.
 */
export function problemFrom(error: ApiError | null, sent: LocationRequest[]): RoleProblem | null {
  if (!error?.field) return null;
  const problem: RoleProblem = { field: error.field, message: error.message };
  if (error.field !== "locations") return problem;

  const entry = error.position != null ? sent[error.position - 1] : undefined;
  if (!entry) return { ...problem, slot: "where" };
  if (entry.mode === "onsite") return { ...problem, slot: "onsite", addressId: entry.address_id };
  return { ...problem, slot: "remote" };
}

/** The message for one control, or undefined. */
export function messageFor(problem: RoleProblem | null, field: string): string | undefined {
  return problem && problem.field === field && !problem.slot ? problem.message : undefined;
}

/**
 * Whether this seat may open a role it writes (ADR-0019 decision 11):
 * anybody under any_hirer, otherwise an owner. Unknown until both have loaded,
 * and unknown is no.
 */
export function mayOpen(authority: RoleAuthority | undefined, seat: OrgRole | undefined): boolean {
  if (!authority || !seat) return false;
  return authority === "any_hirer" || seat === "owner";
}
