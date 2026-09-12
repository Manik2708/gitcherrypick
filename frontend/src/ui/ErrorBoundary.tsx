// A crash in one screen should not blank the shell.
//
// This says the failure is the client's, not a refusal from the server, because
// a reader who has just been told "that did not work" will otherwise assume
// they did something wrong or that their data was lost.

import { Component } from "react";
import type { ErrorInfo, ReactNode } from "react";
import { Button, Notice } from "./primitives";

interface State {
  error: Error | null;
}

export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error("screen crashed", error, info.componentStack);
  }

  render(): ReactNode {
    const { error } = this.state;
    if (!error) return this.props.children;

    return (
      <div className="page page--narrow">
        <Notice title="Something broke on this screen">
          <p>{error.message}</p>
          <p>
            This is a bug in the client, not a refusal from the server — nothing you did caused it
            and nothing was saved incorrectly.
          </p>
          <Button variant="ghost" size="sm" onClick={() => this.setState({ error: null })}>
            Try this screen again
          </Button>
        </Notice>
      </div>
    );
  }
}
