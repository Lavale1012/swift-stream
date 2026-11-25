import type { CSSProperties, ReactNode } from "react";

export type StepState = "todo" | "active" | "done" | "error";

interface StepProps {
  number: number;
  title: string;
  state: StepState;
  /** 0–1: how much of the rail below this step is filled. Done steps are full. */
  fill?: number;
  /** Animates the rail while waiting on something with no measurable progress. */
  busy?: boolean;
  children: ReactNode;
}

export function Step({ number, title, state, fill, busy, children }: StepProps) {
  const style = fill === undefined ? undefined : ({ "--fill": fill } as CSSProperties);
  return (
    <li
      className="step"
      data-state={state}
      data-busy={busy || undefined}
      style={style}
      aria-current={state === "active" ? "step" : undefined}
    >
      <span className="step-marker" aria-hidden="true">
        {state === "done" ? <Check /> : state === "error" ? "!" : number}
      </span>
      <h2 className="step-title">
        {title}
        <span className="visually-hidden"> ({LABELS[state]})</span>
      </h2>
      <div className="step-body">{children}</div>
    </li>
  );
}

const LABELS: Record<StepState, string> = {
  todo: "not started",
  active: "in progress",
  done: "done",
  error: "needs attention",
};

function Check() {
  return (
    <svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2.25">
      <path d="M3.5 8.5l3 3 6-7" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
