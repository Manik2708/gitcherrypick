// The small pieces every screen is built from.
//
// Presentational: props in, JSX out. Nothing here fetches, holds server state,
// or knows an endpoint exists — which is what lets any of it be rendered from a
// literal and reviewed without a backend running.
//
// Class names come from design/assets/css/app.css verbatim. The stylesheet is
// the authority on how these look; this file only decides what exists.

import type { ReactNode } from "react";
import type { HirerRef } from "../contract";

/* --- icons ---------------------------------------------------------------- */

/**
 * The design's icons are inline stroked SVG, not a font.
 *
 * Each is 24×24 on a 1.6 stroke so they sit on the same optical weight as Plex
 * Sans. aria-hidden throughout: every icon here sits beside the words it
 * illustrates, so announcing it would read the label twice.
 */
const PATHS: Record<string, ReactNode> = {
  search: (
    <>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m16 16 4.5 4.5" />
    </>
  ),
  board: (
    <>
      <path d="M4 20h16" />
      <rect x="5" y="11" width="4" height="6" rx="1" />
      <rect x="10" y="6" width="4" height="11" rx="1" />
      <rect x="15" y="13" width="4" height="4" rx="1" />
    </>
  ),
  bookmark: <path d="M7 4h10a1 1 0 0 1 1 1v15l-6-3.4L6 20V5a1 1 0 0 1 1-1Z" />,
  list: (
    <>
      <path d="M4 6h16M4 12h10M4 18h7" />
      <circle cx="18" cy="16" r="3" />
    </>
  ),
  check: <path d="m5 12.5 4.5 4.5L19 7.5" />,
  plus: <path d="M12 5v14M5 12h14" />,
  info: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 11v5M12 8h.01" />
    </>
  ),
  warn: (
    <>
      <path d="M12 4.5 3 19.5h18L12 4.5Z" />
      <path d="M12 10v4M12 17h.01" />
    </>
  ),
  slash: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="m6.5 17.5 11-11" />
    </>
  ),
  chev: <path d="m9 6 6 6-6 6" />,
  back: <path d="m14 6-6 6 6 6" />,
  lock: (
    <>
      <rect x="5" y="11" width="14" height="9" rx="2" />
      <path d="M8.5 11V8a3.5 3.5 0 0 1 7 0v3" />
    </>
  ),
  mail: (
    <>
      <rect x="3.5" y="6" width="17" height="12" rx="2" />
      <path d="m4 8 8 5 8-5" />
    </>
  ),
  trash: (
    <>
      <path d="M5 7h14M10 7V5h4v2M7 7l.8 12h8.4L17 7" />
    </>
  ),
  replay: (
    <>
      <path d="M4 12a8 8 0 1 0 3-6.2" />
      <path d="M4 4v4h4" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7.5V12l3 2" />
    </>
  ),
  commit: (
    <>
      <circle cx="7.5" cy="17" r="3.2" />
      <circle cx="16.5" cy="17" r="3.2" />
      <path d="M12 13.8V9.2M12 9.2c0-2.6 1.7-4.6 4.3-5.2M12 9.2C12 6.6 10.3 4.6 7.7 4" />
    </>
  ),
  people: (
    <>
      <circle cx="9" cy="8" r="3.4" />
      <path d="M3.5 19.5a5.5 5.5 0 0 1 11 0" />
      <path d="M16 5.3a3.4 3.4 0 0 1 0 5.4M17.5 14.2a5.5 5.5 0 0 1 3 5.3" />
    </>
  ),
};

export type IconName = keyof typeof PATHS;

export function Icon({ name, size }: { name: IconName; size?: "xs" | "sm" }) {
  return (
    <svg
      className={size ? `ico ico--${size}` : "ico"}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={size === "xs" ? 2 : 1.6}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {PATHS[name]}
    </svg>
  );
}

/* --- text ----------------------------------------------------------------- */

export function Eyebrow({ children }: { children: ReactNode }) {
  return <p className="eyebrow">{children}</p>;
}

export function PageHead({
  eyebrow,
  title,
  lede,
  actions,
}: {
  eyebrow?: string;
  title: string;
  lede?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="page-head">
      <div>
        {eyebrow ? <Eyebrow>{eyebrow}</Eyebrow> : null}
        <h1 className="page-head__title display">{title}</h1>
        {lede ? <p className="page-head__lede">{lede}</p> : null}
      </div>
      {actions ? <div className="page-head__actions">{actions}</div> : null}
    </div>
  );
}

export function SectionTitle({ children, note }: { children: ReactNode; note?: ReactNode }) {
  return (
    <div className="section__head">
      <h2 className="section__title">{children}</h2>
      {note ? <span className="section__note">{note}</span> : null}
    </div>
  );
}

/* --- containers ----------------------------------------------------------- */

export function Card({
  children,
  pad = true,
  className,
  id,
}: {
  children: ReactNode;
  pad?: boolean;
  className?: string;
  /** Set when the card is a link target — ScrollToHash resolves it. */
  id?: string;
}) {
  const classes = ["card", pad ? "card--pad" : "", className ?? ""].filter(Boolean).join(" ");
  return (
    <section className={classes} id={id}>
      {children}
    </section>
  );
}

export type PillTone = "primary" | "secondary" | "cherry" | "violet" | "stale";

export function Pill({ tone, children }: { tone?: PillTone; children: ReactNode }) {
  return (
    <span className={tone ? `pill pill--${tone}` : "pill"}>
      <span className="pill__dot" aria-hidden="true" />
      {children}
    </span>
  );
}

/** A named fact: the label is its own line, never an inline sibling span. */
export function Stat({ n, label, muted }: { n: ReactNode; label: ReactNode; muted?: boolean }) {
  return (
    <span className="stat">
      <span className={muted ? "stat__n stat__n--muted" : "stat__n"}>{n}</span>
      <span className="stat__label">{label}</span>
    </span>
  );
}

export function Meter({ value, max = 100 }: { value: number | null; max?: number }) {
  const pct = value == null ? 0 : Math.max(0, Math.min(100, (value / max) * 100));
  return (
    <span className="meter" aria-hidden="true">
      <span className="meter__fill" style={{ width: `${pct}%` }} />
    </span>
  );
}

/* --- controls ------------------------------------------------------------- */

export function Button({
  children,
  onClick,
  variant,
  size,
  disabled,
  title,
  type = "button",
}: {
  children: ReactNode;
  onClick?: () => void;
  variant?: "primary" | "ghost" | "quiet" | "danger";
  size?: "sm";
  disabled?: boolean;
  title?: string;
  type?: "button" | "submit";
}) {
  // A BUTTON WITH NO VARIANT IS STILL A BUTTON. `.btn` alone sets a size, a
  // weight and a transparent border — every piece of chrome comes from a
  // variant — so a bare <Button> used to render as bold text with padding and
  // nothing else, indistinguishable from a heading. That is a trap rather than
  // a default, and it caught the first person to write one.
  //
  // "ghost" is the ordinary secondary button: a line, a surface, real edges.
  // Anything that genuinely wants no chrome asks for "quiet" and says so.
  const classes = ["btn", `btn--${variant ?? "ghost"}`, size ? `btn--${size}` : ""]
    .filter(Boolean)
    .join(" ");
  return (
    <button type={type} className={classes} onClick={onClick} disabled={disabled} title={title}>
      {children}
    </button>
  );
}

export function Field({
  label,
  help,
  htmlFor,
  children,
}: {
  label: string;
  help?: ReactNode;
  htmlFor?: string;
  children: ReactNode;
}) {
  return (
    <div className="field">
      <label className="field__label" htmlFor={htmlFor}>
        {label}
      </label>
      {children}
      {help ? <p className="field__help">{help}</p> : null}
    </div>
  );
}

/**
 * A checkbox and its label, as ONE component.
 *
 * There were three idioms for this before — `.checkline`, a bare `.od-row`,
 * and an ad-hoc label — and they disagreed about the one thing a reader
 * notices: the label inherited the body size in some places and the small size
 * in others, so two groups of checkboxes on the same screen were typeset
 * differently. That is not a styling nit; it reads as two different kinds of
 * control, and somebody scanning a filter panel pauses on the difference
 * looking for the meaning that is not there.
 *
 * So the markup and the type scale live here, once. Nothing else in the app
 * should write `<input type="checkbox">` directly.
 */
export function Checkbox({
  label,
  help,
  meta,
  checked,
  onChange,
  disabled,
}: {
  label: ReactNode;
  /** A second line, in the same voice as Field's help. */
  help?: ReactNode;
  /** A short right-aligned figure — a count, a score. */
  meta?: ReactNode;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <label className="cbx">
      <input
        className="cbx__box"
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
      />
      <span className="cbx__body">
        <span className="cbx__label">{label}</span>
        {help ? <span className="cbx__help">{help}</span> : null}
      </span>
      {meta ? <span className="cbx__meta">{meta}</span> : null}
    </label>
  );
}

/**
 * A radio and its label. The same component as Checkbox, one attribute apart.
 *
 * It is here rather than left as raw markup because the two appear TOGETHER —
 * the close-a-role dialogue has radios directly beside checkboxes — and
 * typesetting them differently is the exact inconsistency Checkbox exists to
 * remove. A shared class is what keeps them the same control.
 */
export function Radio({
  name,
  label,
  help,
  checked,
  onChange,
  disabled,
}: {
  /** Radios in one group share a name; the browser enforces the exclusivity. */
  name: string;
  label: ReactNode;
  help?: ReactNode;
  checked: boolean;
  onChange: () => void;
  disabled?: boolean;
}) {
  return (
    <label className="cbx">
      <input
        className="cbx__box"
        type="radio"
        name={name}
        checked={checked}
        disabled={disabled}
        onChange={onChange}
      />
      <span className="cbx__body">
        <span className="cbx__label">{label}</span>
        {help ? <span className="cbx__help">{help}</span> : null}
      </span>
    </label>
  );
}

/* --- states --------------------------------------------------------------- */

/**
 * A banner states a fact about the result set that the rows cannot.
 *
 * "Six are hidden" belongs beside the list, not inside it — there is no row to
 * hang it on, and a reader counting rows would otherwise conclude the total
 * was wrong.
 */
export function Banner({
  children,
  tone,
  icon = "info",
}: {
  children: ReactNode;
  tone?: "info";
  icon?: IconName;
}) {
  return (
    <div className={tone ? `banner banner--${tone}` : "banner"}>
      <Icon name={icon} />
      <div className="banner__body">{children}</div>
    </div>
  );
}

export function Notice({ title, children }: { title: ReactNode; children: ReactNode }) {
  return (
    <div className="notice">
      <p className="notice__title">{title}</p>
      <div className="notice__body">{children}</div>
    </div>
  );
}

export function Empty({
  title,
  children,
  icon = "slash",
  actions,
}: {
  title: string;
  children?: ReactNode;
  icon?: IconName;
  actions?: ReactNode;
}) {
  return (
    <div className="empty">
      <span className="empty__icon">
        <Icon name={icon} />
      </span>
      <p className="empty__title">{title}</p>
      {children ? <p className="empty__body">{children}</p> : null}
      {actions ? <div className="empty__actions">{actions}</div> : null}
    </div>
  );
}

export function Loading({ what }: { what: string }) {
  return (
    <div className="empty" role="status" aria-live="polite" aria-busy="true">
      <p className="empty__title">Loading {what}…</p>
    </div>
  );
}

/**
 * Failure shows what the API said, not a generic apology.
 *
 * role="alert" is assertive: a refusal interrupts, because it is the answer to
 * what the reader just asked for.
 */
export function Failure({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="notice" role="alert">
      <p className="notice__title">That did not work</p>
      <div className="notice__body">
        <p>{message}</p>
        {onRetry ? (
          <Button variant="ghost" size="sm" onClick={onRetry}>
            Try again
          </Button>
        ) : null}
      </div>
    </div>
  );
}

/**
 * Who authored something, including someone who has since left.
 *
 * A revoked seat is named and marked departed rather than dropped: they did the
 * work, and a round from two years ago whose author simply vanished would be a
 * falsified record rather than a tidy one (ADR-0016 §9).
 */
export function By({ who, verb = "By" }: { who?: HirerRef | null; verb?: string }) {
  if (!who) return null;
  return (
    <span>
      {verb} {who.display_name}
      <span className="muted"> ({who.username})</span>
      {who.active ? null : <span className="muted"> — no longer here</span>}
    </span>
  );
}

/**
 * A standing prompt for something a person has not done yet.
 *
 * Distinct from Banner, which states a fact about what is on screen. This one
 * is about something ELSEWHERE that needs attention, and it carries the way to
 * get there — a prompt with no route is a nag.
 */
export function Prompt({
  title,
  children,
  action,
}: {
  title: ReactNode;
  children: ReactNode;
  action: ReactNode;
}) {
  return (
    <div className="banner banner--info">
      <Icon name="info" />
      <div className="banner__body">
        <p>
          <b>{title}</b>
        </p>
        <p>{children}</p>
        <p>{action}</p>
      </div>
    </div>
  );
}

/** Two letters, so a row of people is scannable without a photograph. */
export function Avatar({
  name,
  size,
  tint,
  square,
}: {
  name: string;
  size?: "sm" | "lg";
  tint?: number;
  square?: boolean;
}) {
  // Defensive: a principal reaching here with no name is a bug elsewhere, but
  // it is a bug that should show as a blank avatar rather than a white screen.
  const parts = (name ?? "").trim().split(/\s+/).filter(Boolean);
  const first = parts[0] ?? "";
  const last = parts.length > 1 ? (parts[parts.length - 1] ?? "") : "";
  const initials = (
    last ? `${first.charAt(0)}${last.charAt(0)}` : first.slice(0, 2) || "??"
  ).toUpperCase();

  const classes = ["avatar", size ? `avatar--${size}` : "", square ? "avatar--sq" : ""]
    .filter(Boolean)
    .join(" ");

  return (
    <span
      className={classes}
      data-tint={tint == null ? undefined : String(tint)}
      aria-hidden="true"
    >
      {initials}
    </span>
  );
}
