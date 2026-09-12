// A path that leads nowhere.
//
// Redirecting silently to the home page would hide a broken link — and someone
// who followed one from an email deserves to know it was the link that was
// wrong, not their session.

import { Link, useLocation } from "react-router-dom";
import { Empty } from "../ui/primitives";

export function NotFoundPage() {
  const { pathname } = useLocation();

  return (
    <div className="page page--narrow">
      <Empty
        title="Nothing here"
        actions={
          <Link className="btn btn--ghost btn--sm" to="/">
            Go back
          </Link>
        }
      >
        <code className="mono">{pathname}</code> is not a page on this platform. Your session is
        fine — it was the link that was wrong.
      </Empty>
    </div>
  );
}
