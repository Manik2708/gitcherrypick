// Where somebody stands on a round, and the toggles that filter by it.
//
// One module because two screens ask the same question of two different
// shapes: the entries on a round (RoundEntry) and the people already on a role
// (RoleCandidate). The STAGE is the same fact in both — it comes off the
// contact request — and a hirer reading either list is asking a stage-shaped
// question: who still owes me an answer, who said yes.
//
// Deriving it in one place is also what stops the two screens disagreeing.
// `contact_status` is empty both before a round is confirmed and, briefly,
// after — so "staged" is `not notified`, never the empty string alone.

import type { PillTone } from "./primitives";

export type Stage = "staged" | "pending" | "accepted" | "declined" | "expired";

export const STAGES: Array<{ key: Stage; label: string; tone: PillTone }> = [
  { key: "staged", label: "Not contacted", tone: "secondary" },
  { key: "pending", label: "Waiting", tone: "secondary" },
  { key: "accepted", label: "Accepted", tone: "primary" },
  { key: "declined", label: "Declined", tone: "stale" },
  { key: "expired", label: "No answer", tone: "stale" },
];

const BY_KEY = new Map(STAGES.map((s) => [s.key, s]));

/** The label and tone for a stage — the pill on a row, and the toggle. */
export function stageLook(stage: Stage): { label: string; tone: PillTone } {
  return BY_KEY.get(stage) ?? { label: stage, tone: "secondary" };
}

/**
 * Which stage a row is in.
 *
 * `notified_at` decides the first boundary rather than the contact status,
 * because that is the field the removal rule turns on: null means nobody has
 * been told anything.
 */
export function stageOf(
  contactStatus: string | null | undefined,
  notifiedAt: string | null,
): Stage {
  switch (contactStatus) {
    case "accepted":
      return "accepted";
    case "declined":
      return "declined";
    case "expired":
      return "expired";
    case "pending":
      return "pending";
    default:
      return notifiedAt ? "pending" : "staged";
  }
}

/**
 * The filter itself.
 *
 * NOTHING TICKED MEANS NO FILTER, as on the roles list: a control that can be
 * switched into showing an empty list is one somebody switches into by
 * accident and reads as the screen being broken.
 *
 * Only stages that actually occur in the list are offered — a round nobody has
 * declined has no Declined button to press and find empty.
 */
export function StageToggles({
  present,
  selected,
  onChange,
  label,
}: {
  present: Set<Stage>;
  selected: Set<Stage>;
  onChange: (next: Set<Stage>) => void;
  label: string;
}) {
  const offered = STAGES.filter((s) => present.has(s.key));
  if (offered.length < 2) return null;

  return (
    <div className="segmented" role="group" aria-label={label}>
      {offered.map((s) => (
        <button
          key={s.key}
          type="button"
          aria-pressed={selected.has(s.key)}
          onClick={() => {
            const next = new Set(selected);
            if (next.has(s.key)) {
              next.delete(s.key);
            } else {
              next.add(s.key);
            }
            onChange(next);
          }}
        >
          {s.label}
        </button>
      ))}
    </div>
  );
}

/** Rows in the ticked stages, or every row when nothing is ticked. */
export function inStages<T>(rows: T[], selected: Set<Stage>, of: (row: T) => Stage): T[] {
  if (selected.size === 0) return rows;
  return rows.filter((row) => selected.has(of(row)));
}
