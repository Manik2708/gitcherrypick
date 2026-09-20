// Turning a role into a search.
//
// A hirer who has written a role has already answered most of what the filter
// panel asks: where they can employ somebody, how much experience they need,
// what shape of work it is. Re-typing it into the filters is duplicated work
// that also drifts — the search quietly stops matching the job it is for.
//
// THIS IS A CONVENIENCE, NOT A BINDING. What it produces is a starting point
// the hirer then edits: they untick a country, drop a minimum, widen the
// shape. Nothing here is re-applied afterwards and nothing remembers the role,
// because a filter set that silently snapped back to the role would be worse
// than typing it out.
//
// It lives in logic/ rather than in the page because it is a mapping with
// rules worth testing, and a component is the one place those rules cannot be
// read on their own.

import type { Role } from "../contract";
import type { SearchFilters } from "../hooks/hirer";

/** The subset of a search a role can answer. */
export type DerivedFilters = Pick<
  SearchFilters,
  "countries" | "minOfficeYoe" | "minOssYoe" | "openTo" | "forRole"
>;

/**
 * What shapes of work a role implies.
 *
 * `open_to` is OR-ed by the server, so naming more shapes WIDENS the search.
 * That is why an engagement maps to as few as possible: a contract role asks
 * for people open to contract work, and adding "remote" alongside it would
 * also return everybody open to a permanent remote job.
 *
 * Only a permanent role reads the location, because "full time" alone says
 * nothing about where — and remote and onsite are the two flags that do.
 */
function shapesFor(role: Role): string[] {
  switch (role.engagement) {
    case "full_time":
      return role.location === "remote" ? ["remote"] : ["onsite"];
    case "contract":
      return ["contract"];
    case "internship":
      return ["internship"];
    case "freelance":
      // A preference like every other engagement now (ADR-0021 §5). It used
      // to narrow availability instead, because there was no flag for it —
      // which made freelance the one shape expressed differently from the
      // other four, and put an exception in three separate places.
      return ["freelance"];
  }
}

/**
 * The filters a role implies.
 *
 * Fields the role does not state come back UNDEFINED rather than omitted, so
 * applying a second role clears what the first one set. A minimum left behind
 * from a previous role is the failure mode worth avoiding here: it silently
 * narrows a search the hirer believes they have just reset.
 */
export function filtersFromRole(role: Role): DerivedFilters {
  return {
    // Empty means ANYWHERE on a role, and no filter in a search — the same
    // thing said two ways, so it maps to undefined rather than [].
    countries: role.eligible_countries.length > 0 ? [...role.eligible_countries] : undefined,
    minOfficeYoe: role.min_office_yoe ?? undefined,
    minOssYoe: role.min_oss_yoe ?? undefined,
    openTo: shapesFor(role).length > 0 ? shapesFor(role) : undefined,

    // And stop offering the people already on a round for it. Searching again
    // for a job you have been working is the case this whole feature is for,
    // and re-reading names you have already staged is the waste it removes.
    forRole: role.id,
  };
}

/**
 * Applies a role over an existing filter set.
 *
 * Everything a role cannot answer is LEFT ALONE — the skills, the name box,
 * the score thresholds, the page size. A role says nothing about which skills
 * a hirer wants, and wiping a carefully built skill list because somebody
 * picked a role from a dropdown would be the opposite of a convenience.
 */
export function applyRole(current: SearchFilters, role: Role): SearchFilters {
  return { ...current, ...filtersFromRole(role), page: 1 };
}
