// The small pieces every screen is built from.
//
// Presentational: props in, JSX out. Nothing here fetches, holds server state,
// or knows an endpoint exists — which is what lets any of it be rendered from a
// literal and reviewed without a backend running.
//
// Class names come from design/assets/css/app.css verbatim. The stylesheet is
// the authority on how these look; this file only decides what exists.

import type { ReactNode } from "react";

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
export function Stat({
  n,
  label,
  muted,
}: {
  n: ReactNode;
  label: ReactNode;
  muted?: boolean;
}) {
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
  const classes = ["btn", variant ? `btn--${variant}` : "", size ? `btn--${size}` : ""]
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
  const parts = name.trim().split(/\s+/).filter(Boolean);
  const first = parts[0] ?? "";
  const last = parts.length > 1 ? (parts[parts.length - 1] ?? "") : "";
  const initials = (
    last ? `${first.charAt(0)}${last.charAt(0)}` : first.slice(0, 2) || "??"
  ).toUpperCase();

  const classes = ["avatar", size ? `avatar--${size}` : "", square ? "avatar--sq" : ""]
    .filter(Boolean)
    .join(" ");

  return (
    <span className={classes} data-tint={tint == null ? undefined : String(tint)} aria-hidden="true">
      {initials}
    </span>
  );
}
