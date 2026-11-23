import type { ReactNode } from 'react';

import { cx } from '../cx';

export interface NoticeProps {
  /** error is announced at once (role="alert"); info waits for a pause (role="status"). */
  tone?: 'info' | 'error';
  className?: string;
  children: ReactNode;
}

/** A short message about the current view: a failed request, an empty result. */
export function Notice({ tone = 'info', className, children }: NoticeProps) {
  return (
    <p
      role={tone === 'error' ? 'alert' : 'status'}
      className={cx('text-sm', tone === 'error' ? 'text-danger' : 'text-muted-foreground', className)}
    >
      {children}
    </p>
  );
}
