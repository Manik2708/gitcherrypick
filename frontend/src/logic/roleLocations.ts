// Where a role's work is (ADR-0024).
//
// A role holds a set of entries of three kinds: remote from anywhere, remote
// within a country, onsite at one of the organisation's addresses. The form
// edits them as two switches — Remote and Onsite — because that is how a hirer
// thinks about it, and this file turns one into the other in both directions.
//
// The order entries are SENT in matters: a refusal names an entry by its
// 1-based position in the request (amendment 1 §1.6), and the server reorders
// what it stores, so a position is only meaningful against what was sent.

import type {
  ContributorLocation,
  LocationRequest,
  OrgAddress,
  RoleLocationEntry,
} from "../contract";

/** The form's view of a role's locations. */
export interface LocationChoice {
  remote: boolean;
  /** Remote from anywhere, or only in `countries`. Never both (decision 3). */
  anywhere: boolean;
  countries: string[];
  onsite: boolean;
  addressIds: string[];
}

/** A new role starts remote from anywhere, which every organisation can post. */
export const DEFAULT_CHOICE: LocationChoice = {
  remote: true,
  anywhere: true,
  countries: [],
  onsite: false,
  addressIds: [],
};

/** The form's view of a role already written. */
export function choiceOf(entries: RoleLocationEntry[]): LocationChoice {
  const remote = entries.filter((e) => e.mode === "remote");
  const countries = remote.map((e) => e.country).filter((c): c is string => Boolean(c));
  const onsite = entries.filter((e) => e.mode === "onsite" && e.address);
  return {
    remote: remote.length > 0,
    anywhere: remote.length === 0 || countries.length === 0,
    countries,
    onsite: onsite.length > 0,
    addressIds: onsite.map((e) => (e.address as OrgAddress).id),
  };
}

/** What the request carries: remote entries first, then onsite, in the form's order. */
export function locationsOf(choice: LocationChoice): LocationRequest[] {
  const out: LocationRequest[] = [];
  if (choice.remote) {
    if (choice.anywhere) {
      out.push({ mode: "remote" });
    } else {
      for (const country of choice.countries) out.push({ mode: "remote", country });
    }
  }
  if (choice.onsite) {
    for (const id of choice.addressIds) out.push({ mode: "onsite", address_id: id });
  }
  return out;
}

/** One place, at whatever detail the reader is allowed. */
interface Place {
  mode: "remote" | "onsite";
  country: string | null;
  city: string | null;
  removed: boolean;
}

function phrase(places: Place[], nameOf: (code: string) => string): string {
  const parts: string[] = [];

  const remote = places.filter((p) => p.mode === "remote");
  if (remote.some((p) => p.country == null)) {
    parts.push("Remote from anywhere");
  } else if (remote.length > 0) {
    const names = [...new Set(remote.map((p) => p.country as string))].map(nameOf);
    parts.push(`Remote in ${names.join(", ")}`);
  }

  const onsite = places.filter((p) => p.mode === "onsite");
  if (onsite.length > 0) {
    const labels = onsite.map((p) => {
      const where = [p.city, p.country ? nameOf(p.country) : null].filter(Boolean).join(", ");
      return p.removed ? `${where} (removed)` : where;
    });
    parts.push(`Onsite in ${labels.join("; ")}`);
  }

  return parts.join(" · ");
}

/** A role's locations as its own organisation reads them. */
export function describeForHirer(
  entries: RoleLocationEntry[],
  nameOf: (code: string) => string,
): string {
  return phrase(
    entries.map((e) => ({
      mode: e.mode,
      country: e.mode === "remote" ? e.country : (e.address?.country ?? null),
      city: e.address?.city ?? null,
      removed: e.address?.removed ?? false,
    })),
    nameOf,
  );
}

/** A role's locations as a contributor reads them: a city and a country, never a street. */
export function describeForContributor(
  locations: ContributorLocation[],
  nameOf: (code: string) => string,
): string {
  return phrase(
    locations.map((l) => ({
      mode: l.mode,
      country: l.country,
      city: l.mode === "onsite" ? l.city : null,
      removed: false,
    })),
    nameOf,
  );
}

/** An address on one line, for the organisation that owns it. */
export function addressLine(address: {
  street1: string;
  street2: string;
  city: string;
  postal_code: string;
  country: string;
}): string {
  return [address.street1, address.street2, address.city, address.postal_code, address.country]
    .filter(Boolean)
    .join(", ");
}
