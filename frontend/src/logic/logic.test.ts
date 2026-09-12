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
