import type { ReactNode } from 'react';

import { cx } from '../cx';

export type BadgeTone = 'neutral' | 'active' | 'success' | 'danger';

export interface BadgeProps {
  /** neutral: waiting or unknown. active: in progress. success: done. danger: failed. */
  tone?: BadgeTone;
  className?: string;
  children: ReactNode;
}

const tones: Record<BadgeTone, string> = {
  neutral: 'text-muted-foreground border-border',
  active: 'text-foreground border-input',
  success: 'text-success border-success',
  danger: 'text-danger border-danger',
};

/** A short status label. The text carries the meaning; the colour only repeats it. */
export function Badge({ tone = 'neutral', className, children }: BadgeProps) {
  return (
    <span
      className={cx(
        'bg-surface inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap',
        tones[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}
