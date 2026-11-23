import type { ButtonHTMLAttributes } from 'react';

import { cx } from '../cx';

export type ButtonVariant = 'primary' | 'secondary' | 'danger';

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  /** primary: the one main action in a view. secondary: everything else. danger: deletes or ends something. */
  variant?: ButtonVariant;
  /** Stretch to the container's width. */
  fullWidth?: boolean;
}

const variants: Record<ButtonVariant, string> = {
  primary: 'bg-primary text-primary-foreground border-primary hover:bg-primary-hover',
  secondary: 'bg-surface text-foreground border-input hover:bg-secondary-hover',
  danger: 'bg-danger text-danger-foreground border-danger hover:bg-danger-hover',
};

/** A button. `type` defaults to "button", so it never submits a form by accident. */
export function Button({ variant = 'secondary', fullWidth = false, type = 'button', className, ...props }: ButtonProps) {
  return (
    <button
      type={type}
      className={cx(
        'rounded-control inline-flex min-h-11 items-center justify-center gap-2 border px-4 text-sm font-medium',
        'focus-visible:outline-ring focus-visible:outline-2 focus-visible:outline-offset-2',
        'disabled:cursor-not-allowed disabled:opacity-50',
        variants[variant],
        fullWidth && 'w-full',
        className,
      )}
      {...props}
    />
  );
}
