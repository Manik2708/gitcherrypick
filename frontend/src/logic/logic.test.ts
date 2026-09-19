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
import { applyRole, filtersFromRole } from "./roleFilters";
import type { Role } from "../contract";

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

const ROLE: Role = {
  id: "r1",
  organization_id: "o1",
  status: "open",
  title: "Senior platform engineer",
  description: "",
  engagement: "full_time",
  location: "remote",
  address_id: null,
  currency: "GBP",
  yearly_ctc: 11500000,
  yearly_base: 9500000,
  hourly_rate: null,
  expected_hours: null,
  eligible_countries: ["GB", "DE"],
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
      countries: ["GB", "DE"],
      minOfficeYoe: 5,
      minOssYoe: 3,
      openTo: ["remote"],
      availability: undefined,
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
    expect(filtersFromRole({ ...ROLE, location: "address" }).openTo).toEqual(["onsite"]);
  });

  it("narrows AVAILABILITY for freelance rather than inventing a flag", () => {
    // There is no open_to_freelance flag: availability_status already carries
    // freelance twice over, and two controls meaning one thing can disagree.
    const freelance = filtersFromRole({ ...ROLE, engagement: "freelance" });
    expect(freelance.openTo).toBeUndefined();
    expect(freelance.availability).toEqual(["looking_for_freelance", "open_to_freelance"]);
  });

  it("treats no eligible countries as no filter, not as nowhere", () => {
    expect(filtersFromRole({ ...ROLE, eligible_countries: [] }).countries).toBeUndefined();
  });

  it("CLEARS what a previous role set", () => {
    // The failure worth avoiding: a minimum left behind from an earlier role
    // silently narrows a search the hirer believes they have just repointed.
    const after = applyRole(
      { minOfficeYoe: 9, minOssYoe: 9, countries: ["IN"], openTo: ["contract"] },
      { ...ROLE, min_office_yoe: null, min_oss_yoe: null, eligible_countries: [] },
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
