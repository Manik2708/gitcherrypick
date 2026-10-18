// The rules, tested without a browser or a server.

import { describe, expect, it } from "vitest";
import {
  claimStatus,
  dimensionLabel,
  filledSlots,
  isLocked,
  orderedDimensions,
  promotionDistance,
  readyToSubmit,
  rejectionReason,
  slotsFromEvidence,
} from "./claims";
import * as format from "./format";
import { draftProblem, mayOpen, messageFor, problemFrom } from "./roleDraft";
import { applyRole, filtersFromRole } from "./roleFilters";
import {
  DEFAULT_CHOICE,
  choiceOf,
  describeForContributor,
  describeForHirer,
  locationsOf,
} from "./roleLocations";
import { ApiError, defaultMessage } from "../api/http";
import type { OrgAddress, Role, RoleLocationEntry } from "../contract";

const NOW = new Date("2026-08-12T09:00:00Z");

describe("null is not zero", () => {
  it("says an unmeasured score is unmeasured", () => {
    expect(format.score(null)).toBe("not measured");
    expect(format.n1(null)).toBe("—");
    expect(format.n1(0)).toBe("0.0");
  });

  it("distinguishes unranked from rank zero", () => {
    expect(format.rank(null)).toBe("unranked");
    expect(format.rank(17)).toBe("#17");
  });
});

describe("evidence slots", () => {
  it("puts each PR in the slot its position names", () => {
    const slots = slotsFromEvidence([
      { position: 3, repo_owner: "grafana", repo_name: "loki", pr_number: 9981 },
      { position: 1, repo_owner: "golang", repo_name: "go", pr_number: 61234 },
    ]);
    expect(slots).toHaveLength(5);
    expect(slots[0]).toBe("https://github.com/golang/go/pull/61234");
    expect(slots[1]).toBe("");
    expect(slots[2]).toBe("https://github.com/grafana/loki/pull/9981");
  });

  // The whole reason the editor is five slots: a gap is a gap, and PR 3 keeps
  // being PR 3 when PR 2 is cleared. Renumbering would point every server-side
  // "PR 3: …" refusal at the wrong row.
  it("leaves a hole rather than closing it", () => {
    const slots = slotsFromEvidence([
      { position: 1, repo_owner: "a", repo_name: "b", pr_number: 1 },
      { position: 3, repo_owner: "c", repo_name: "d", pr_number: 3 },
    ]);
    expect(slots[1]).toBe("");
    expect(filledSlots(slots)).toBe(2);
  });

  it("ignores a position outside the bound, and unresolved evidence", () => {
    expect(
      filledSlots(
        slotsFromEvidence([
          { position: 9, repo_owner: "a", repo_name: "b", pr_number: 1 },
          { position: 2, repo_owner: null, repo_name: null, pr_number: null },
        ]),
      ),
    ).toBe(0);
  });
});

describe("claims", () => {
  it("names each status the way the product does", () => {
    expect(claimStatus("queued")).toBe("Awaiting judgement");
    expect(claimStatus("evaluated")).toBe("Judged");
    expect(claimStatus("invalid")).toBe("Needs attention");
  });

  it("locks a judged claim until its freeze expires", () => {
    expect(isLocked({ locked_until: "2026-08-17T09:00:00Z" }, NOW)).toBe(true);
    expect(isLocked({ locked_until: "2026-08-01T09:00:00Z" }, NOW)).toBe(false);
    expect(isLocked({ locked_until: null }, NOW)).toBe(false);
  });

  it("says what a claim still needs", () => {
    const empty = readyToSubmit({ id: "c", status: "draft", version: 1 });
    expect(empty.ready).toBe(false);
    expect(empty.reasons).toContain("Add at least one pull request.");
    expect(empty.reasons).toContain("Declare at least one skill.");
  });

  it("explains a dropped PR rather than showing a code", () => {
    expect(rejectionReason("not_merged")).toBe("Not merged.");
    expect(rejectionReason(undefined)).toBe("Did not count.");
  });
});

describe("promotion", () => {
  it("counts the distance to primary", () => {
    expect(promotionDistance(3)).toEqual({ have: 3, need: 5, remaining: 2 });
    expect(promotionDistance(5).remaining).toBe(0);
    // Never negative: a skill past the threshold is simply promoted.
    expect(promotionDistance(7).remaining).toBe(0);
  });
});

describe("dimensions", () => {
  it("orders the five judged dimensions by rubric weight", () => {
    const ordered = orderedDimensions({
      craft: 1,
      substance: 2,
      skill_specificity: 3,
      complexity: 4,
      conversation_quality: 5,
    });
    expect(ordered.map(([n]) => n)).toEqual([
      "substance",
      "complexity",
      "conversation_quality",
      "craft",
      "skill_specificity",
    ]);
  });

  it("labels conversation_quality the way the product does", () => {
    expect(dimensionLabel("conversation_quality")).toBe("Conversation");
  });
});

/* --- filling the search from a role (frontend-only convenience) ----------- */

const BENGALURU: OrgAddress = {
  id: "ad1",
  country: "IN",
  city: "Bengaluru",
  postal_code: "560001",
  street1: "1 MG Road",
  street2: "Floor 4",
  is_main_office: true,
  removed: false,
};

const AUSTIN: OrgAddress = {
  ...BENGALURU,
  id: "ad2",
  country: "US",
  city: "Austin",
  is_main_office: false,
};

const ANYWHERE: RoleLocationEntry = { mode: "remote", country: null, address: null };
const IN_INDIA: RoleLocationEntry = { mode: "remote", country: "IN", address: null };
const AT = (address: OrgAddress): RoleLocationEntry => ({ mode: "onsite", country: null, address });

const ROLE: Role = {
  id: "r1",
  organization_id: "o1",
  status: "open",
  title: "Senior platform engineer",
  description: "",
  engagement: "full_time",
  locations: [
    { mode: "remote", country: "DE", address: null },
    { mode: "remote", country: "GB", address: null },
  ],
  currency: "GBP",
  yearly_ctc: 11500000,
  yearly_base: 9500000,
  hourly_rate: null,
  expected_hours: null,
  min_office_yoe: 5,
  min_oss_yoe: 3,
  requires_online_test: false,
  max_interview_rounds: 3,
  avg_days_to_offer: 12,
  questions: [],
  opened_at: "2026-09-16T00:00:00Z",
  close_requested_at: null,
  closed_at: null,
  close_reason: null,
  hires: [],
  advertised: false,
  supersedes: null,
  created_at: "2026-09-16T00:00:00Z",
  updated_at: "2026-09-16T00:00:00Z",
};

describe("a role fills the search in", () => {
  it("carries across what a role actually states", () => {
    expect(filtersFromRole(ROLE)).toEqual({
      countries: ["DE", "GB"],
      minOfficeYoe: 5,
      minOssYoe: 3,
      openTo: ["remote"],
      // And the exclusion: searching again for a job you have been working
      // should not keep offering the people already on a round for it.
      forRole: "r1",
    });
  });

  it("maps an engagement to as FEW shapes as possible", () => {
    // open_to is OR-ed by the server, so naming more shapes widens the search.
    // A contract role that also asked for "remote" would return everybody open
    // to a permanent remote job.
    expect(filtersFromRole({ ...ROLE, engagement: "contract" }).openTo).toEqual(["contract"]);
    expect(filtersFromRole({ ...ROLE, engagement: "internship" }).openTo).toEqual(["internship"]);
    expect(filtersFromRole({ ...ROLE, locations: [AT(BENGALURU)] }).openTo).toEqual(["onsite"]);
  });

  it("yields every mode a role's locations have, OR-ed (ADR-0021 amendment 1)", () => {
    const mixed = { ...ROLE, locations: [IN_INDIA, AT(BENGALURU)] };
    expect(filtersFromRole(mixed).openTo).toEqual(["remote", "onsite"]);
    // The engagement still wins for everything but full time.
    expect(filtersFromRole({ ...mixed, engagement: "contract" }).openTo).toEqual(["contract"]);
  });

  it("maps freelance to a preference like every other engagement", () => {
    // It used to narrow availability instead, because there was no flag —
    // which made freelance the one shape expressed differently from the other
    // four, and put the same exception in three separate places (ADR-0021 §5).
    expect(filtersFromRole({ ...ROLE, engagement: "freelance" }).openTo).toEqual(["freelance"]);
  });

  it("filters by country only when every entry is remote in a named country", () => {
    // Remote-anywhere and onsite admit everybody (ADR-0024 decision 5), so
    // one of either beside a country entry means no country filter at all.
    expect(filtersFromRole({ ...ROLE, locations: [ANYWHERE] }).countries).toBeUndefined();
    expect(
      filtersFromRole({ ...ROLE, locations: [IN_INDIA, AT(BENGALURU)] }).countries,
    ).toBeUndefined();
    expect(filtersFromRole({ ...ROLE, locations: [AT(AUSTIN)] }).countries).toBeUndefined();
    expect(filtersFromRole({ ...ROLE, locations: [IN_INDIA] }).countries).toEqual(["IN"]);
  });

  it("CLEARS what a previous role set", () => {
    // The failure worth avoiding: a minimum left behind from an earlier role
    // silently narrows a search the hirer believes they have just repointed.
    const after = applyRole(
      { minOfficeYoe: 9, minOssYoe: 9, countries: ["IN"], openTo: ["contract"] },
      { ...ROLE, min_office_yoe: null, min_oss_yoe: null, locations: [ANYWHERE] },
    );
    expect(after.minOfficeYoe).toBeUndefined();
    expect(after.minOssYoe).toBeUndefined();
    expect(after.countries).toBeUndefined();
    expect(after.openTo).toEqual(["remote"]);
  });

  it("leaves alone everything a role cannot answer", () => {
    // A role says nothing about which skills somebody wants. Wiping a skill
    // list because a role was picked from a dropdown is the opposite of a
    // convenience.
    const after = applyRole({ skills: ["go", "kubernetes"], q: "ada", minSkillScore: 70 }, ROLE);
    expect(after.skills).toEqual(["go", "kubernetes"]);
    expect(after.q).toBe("ada");
    expect(after.minSkillScore).toBe(70);
  });

  it("returns to the first page, because the population just changed", () => {
    expect(applyRole({ page: 4 }, ROLE).page).toBe(1);
  });

  it("points the exclusion at the role it was filled from", () => {
    // Applying a SECOND role must repoint it, not leave the first one
    // quietly excluding people from a search about a different job.
    const after = applyRole({ forRole: "an-older-role" }, ROLE);
    expect(after.forRole).toBe("r1");
  });
});

describe("a role's locations, as a form and as a request", () => {
  it("sends remote entries first, then onsite, in the form's order", () => {
    expect(
      locationsOf({
        remote: true,
        anywhere: false,
        countries: ["PT", "IN"],
        onsite: true,
        addressIds: ["ad2", "ad1"],
      }),
    ).toEqual([
      { mode: "remote", country: "PT" },
      { mode: "remote", country: "IN" },
      { mode: "onsite", address_id: "ad2" },
      { mode: "onsite", address_id: "ad1" },
    ]);
  });

  it("never sends remote-anywhere beside a country (decision 3)", () => {
    // Countries picked and then "anywhere" chosen: the countries are kept in
    // the form, in case they switch back, but not sent.
    expect(locationsOf({ ...DEFAULT_CHOICE, anywhere: true, countries: ["IN"] })).toEqual([
      { mode: "remote" },
    ]);
  });

  it("starts a new role remote from anywhere", () => {
    expect(locationsOf(DEFAULT_CHOICE)).toEqual([{ mode: "remote" }]);
  });

  it("reads a written role back into the form", () => {
    expect(choiceOf([IN_INDIA, AT(BENGALURU)])).toEqual({
      remote: true,
      anywhere: false,
      countries: ["IN"],
      onsite: true,
      addressIds: ["ad1"],
    });
    expect(choiceOf([AT(AUSTIN)])).toMatchObject({ remote: false, onsite: true });
    expect(choiceOf([ANYWHERE])).toMatchObject({ remote: true, anywhere: true, onsite: false });
  });

  const names = (code: string) =>
    ({ IN: "India", US: "United States", GB: "United Kingdom" })[code] ?? code;

  it("tells a hirer the city and country, and marks a removed office", () => {
    expect(describeForHirer([ANYWHERE], names)).toBe("Remote from anywhere");
    expect(
      describeForHirer([IN_INDIA, AT(BENGALURU), AT({ ...AUSTIN, removed: true })], names),
    ).toBe("Remote in India · Onsite in Bengaluru, India; Austin, United States (removed)");
  });

  it("tells a contributor a city and a country, never a street", () => {
    const told = describeForContributor(
      [
        { mode: "remote", country: "GB" },
        { mode: "onsite", country: "IN", city: "Bengaluru" },
      ],
      names,
    );
    expect(told).toBe("Remote in United Kingdom · Onsite in Bengaluru, India");
    expect(told).not.toMatch(/MG Road|560001/);
  });

  it("falls back to the code when a country has no name", () => {
    expect(describeForContributor([{ mode: "remote", country: "ZZ" }], names)).toBe("Remote in ZZ");
  });
});

describe("a role is checked before it is sent", () => {
  it("accepts a title and somewhere to work", () => {
    expect(draftProblem("Backend engineer", DEFAULT_CHOICE)).toBeNull();
  });

  it("wants a title", () => {
    expect(draftProblem("  ", DEFAULT_CHOICE)).toMatchObject({ field: "title" });
  });

  it("wants somewhere for the work to happen", () => {
    const nowhere = { ...DEFAULT_CHOICE, remote: false };
    expect(draftProblem("Backend engineer", nowhere)).toMatchObject({
      field: "locations",
      slot: "where",
    });
  });

  it("wants a country once remote is limited to some", () => {
    const none = { ...DEFAULT_CHOICE, anywhere: false, countries: [] };
    expect(draftProblem("Backend engineer", none)).toMatchObject({ slot: "remote" });
    expect(draftProblem("Backend engineer", { ...none, countries: ["IN"] })).toBeNull();
  });

  it("wants an office for onsite work", () => {
    const onsite = { ...DEFAULT_CHOICE, remote: false, onsite: true };
    expect(draftProblem("Backend engineer", onsite)).toMatchObject({ slot: "onsite" });
    expect(draftProblem("Backend engineer", { ...onsite, addressIds: ["ad1"] })).toBeNull();
  });
});

describe("a refusal is placed beside the control it names", () => {
  const refusal = (code: string, body: Record<string, unknown>) =>
    new ApiError(422, code, defaultMessage(code, body.field as string | undefined), {
      error: code,
      ...body,
    });

  const sent = [
    { mode: "remote" as const, country: "GB" },
    { mode: "onsite" as const, address_id: "ad1" },
  ];

  it("reads a position against the request that was sent, 1-based", () => {
    const problem = problemFrom(
      refusal("address_removed", { field: "locations", position: 2 }),
      sent,
    );
    expect(problem).toMatchObject({ slot: "onsite", addressId: "ad1" });
    expect(problem?.message).toBe("That office has been removed.");

    expect(
      problemFrom(refusal("role_location_invalid", { field: "locations", position: 1 }), sent),
    ).toMatchObject({ slot: "remote" });
  });

  it("puts a refusal with no position on the whole location set", () => {
    expect(
      problemFrom(refusal("role_location_required", { field: "locations" }), []),
    ).toMatchObject({ slot: "where", message: "Say where the work happens." });
  });

  it("puts an ordinary field refusal on its control", () => {
    const problem = problemFrom(refusal("invalid_role", { field: "yearly_base" }), []);
    expect(messageFor(problem, "yearly_base")).toMatch(/base/);
    expect(messageFor(problem, "yearly_ctc")).toBeUndefined();
  });

  it("leaves a refusal naming no field to the form as a whole", () => {
    expect(problemFrom(refusal("forbidden", {}), [])).toBeNull();
    expect(problemFrom(null, [])).toBeNull();
  });
});

describe("refusals read as plain words", () => {
  it("chooses the sentence by field where one code covers several controls", () => {
    expect(defaultMessage("invalid_role", "currency")).toBe("Pick a currency for the amount.");
    expect(defaultMessage("invalid_address", "street1")).toBe("Give a street.");
    expect(defaultMessage("invalid_onboarding", "address.city")).toBe("Give the office's city.");
    expect(defaultMessage("invalid_onboarding", "workplace")).toMatch(/office or are fully remote/);
  });

  it("has a sentence for every ADR-0024 code", () => {
    for (const code of [
      "role_location_required",
      "role_location_invalid",
      "onsite_needs_address",
      "remote_anywhere_exclusive",
      "address_not_found",
      "address_removed",
      "address_in_use",
      "main_office_not_removable",
      "organization_fully_remote",
      "office_request_pending",
      "organization_has_office",
      "onsite_roles_exist",
      "request_already_decided",
    ]) {
      expect(defaultMessage(code)).not.toBe("That did not work.");
    }
  });

  it("reads field, position and the blocking roles off the body", () => {
    const error = new ApiError(409, "address_in_use", "", {
      error: "address_in_use",
      roles: [
        { id: "r1", title: "Austin field engineer", status: "draft" },
        { id: "r2", title: "Austin support lead", status: "open" },
        { id: "r3" },
      ],
    });
    expect(error.roles.map((r) => r.title)).toEqual([
      "Austin field engineer",
      "Austin support lead",
    ]);
    expect(error.field).toBeUndefined();

    const positioned = new ApiError(422, "onsite_needs_address", "", {
      field: "locations",
      position: 2,
    });
    expect(positioned.field).toBe("locations");
    expect(positioned.position).toBe(2);
  });
});

describe("who may open a role from the form", () => {
  it("lets anybody under any_hirer", () => {
    expect(mayOpen("any_hirer", "member")).toBe(true);
  });

  it("lets only an owner otherwise", () => {
    expect(mayOpen("draft_and_approve", "member")).toBe(false);
    expect(mayOpen("draft_and_approve", "owner")).toBe(true);
    expect(mayOpen("owners_only", "owner")).toBe(true);
  });

  it("offers nothing until the settings and the seat have loaded", () => {
    expect(mayOpen(undefined, "owner")).toBe(false);
    expect(mayOpen("any_hirer", undefined)).toBe(false);
  });
});
