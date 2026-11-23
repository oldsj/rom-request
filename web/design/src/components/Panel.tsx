import type { HTMLAttributes, ReactNode } from 'react';

import { cx } from '../cx';

export interface PanelProps extends HTMLAttributes<HTMLElement> {
  /** The element to render: a list item when panels are in a list. */
  as?: 'section' | 'article' | 'div' | 'li';
  /** Optional heading, rendered as an h2 that names the panel. */
  heading?: ReactNode;
  /** Optional one-sentence description under the heading. */
  description?: ReactNode;
}

/** A bordered surface that groups related content. Don't nest panels. */
export function Panel({ as: Tag = 'section', heading, description, className, children, ...props }: PanelProps) {
  return (
    <Tag className={cx('bg-surface border-border rounded-panel border p-4', className)} {...props}>
      {heading && <h2 className="text-base font-semibold">{heading}</h2>}
      {description && <p className="text-muted-foreground mt-1 text-sm">{description}</p>}
      {(heading ?? description) ? <div className="mt-3">{children}</div> : children}
    </Tag>
  );
}
