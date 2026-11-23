import { useId, type ReactNode } from 'react';

import { cx } from '../cx';

export interface ProgressProps {
  /** Names the bar for assistive technology, and is shown above it. */
  label: ReactNode;
  /** Done, from 0 to 1. */
  value: number;
  className?: string;
}

/** A labelled progress bar with its percentage. A native <progress>, so no inline styles. */
export function Progress({ label, value, className }: ProgressProps) {
  const id = useId();
  const clamped = Math.min(1, Math.max(0, value));
  const percent = `${String(Math.floor(clamped * 100))}%`;
  return (
    <div className={className}>
      <div className="text-muted-foreground flex justify-between text-sm">
        <label htmlFor={id}>{label}</label>
        <span aria-hidden="true">{percent}</span>
      </div>
      <progress
        id={id}
        max={1}
        value={clamped}
        aria-valuetext={percent}
        className={cx(
          'bg-surface-muted border-input mt-1 block h-2 w-full appearance-none overflow-hidden rounded-full border',
          '[&::-webkit-progress-bar]:bg-surface-muted [&::-webkit-progress-value]:bg-primary [&::-moz-progress-bar]:bg-primary',
        )}
      />
    </div>
  );
}
