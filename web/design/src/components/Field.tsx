import {
  useId,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from 'react';

import { cx } from '../cx';

interface FieldProps {
  /** Visible label. Required: placeholders are not labels. */
  label: ReactNode;
  /** One short sentence under the label, read with the control. */
  hint?: ReactNode;
  /** What is wrong and how to fix it. Marks the control invalid. */
  error?: ReactNode;
}

export type InputProps = FieldProps & Omit<InputHTMLAttributes<HTMLInputElement>, 'children'>;
export type TextareaProps = FieldProps & Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'children'>;
export type SelectProps = FieldProps & SelectHTMLAttributes<HTMLSelectElement>;

const control = cx(
  'rounded-control bg-surface text-foreground border-input mt-1 block w-full border px-3 py-2',
  'placeholder:text-muted-foreground min-h-11',
  'focus-visible:outline-ring focus-visible:outline-2 focus-visible:outline-offset-1',
  'aria-invalid:border-danger disabled:cursor-not-allowed disabled:opacity-50',
);

function useField({ hint, error }: Pick<FieldProps, 'hint' | 'error'>, id: string | undefined) {
  const auto = useId();
  const controlId = id ?? auto;
  const hintId = hint ? `${controlId}-hint` : undefined;
  const errorId = error ? `${controlId}-error` : undefined;
  return {
    controlId,
    hintId,
    errorId,
    describedBy: [hintId, errorId].filter(Boolean).join(' ') || undefined,
  };
}

function Shell({ label, hint, error, controlId, hintId, errorId, className, children }: FieldProps & {
  controlId: string;
  hintId: string | undefined;
  errorId: string | undefined;
  className: string | undefined;
  children: ReactNode;
}) {
  return (
    <div className={className}>
      <label htmlFor={controlId} className="block text-sm font-medium">
        {label}
      </label>
      {hint && (
        <p id={hintId} className="text-muted-foreground mt-0.5 text-sm">
          {hint}
        </p>
      )}
      {children}
      {error && (
        <p id={errorId} className="text-danger mt-1 text-sm">
          {error}
        </p>
      )}
    </div>
  );
}

/** A labelled text input with an optional hint and error. `className` styles the wrapper. */
export function Input({ label, hint, error, id, className, ...props }: InputProps) {
  const f = useField({ hint, error }, id);
  return (
    <Shell {...{ label, hint, error, className, ...f }}>
      <input
        id={f.controlId}
        aria-describedby={f.describedBy}
        aria-invalid={error ? true : undefined}
        className={control}
        {...props}
      />
    </Shell>
  );
}

/** A labelled multi-line text input, otherwise the same as Input. */
export function Textarea({ label, hint, error, id, className, rows = 3, ...props }: TextareaProps) {
  const f = useField({ hint, error }, id);
  return (
    <Shell {...{ label, hint, error, className, ...f }}>
      <textarea
        id={f.controlId}
        rows={rows}
        aria-describedby={f.describedBy}
        aria-invalid={error ? true : undefined}
        className={control}
        {...props}
      />
    </Shell>
  );
}

/** A labelled native select; pass <option> children. Otherwise the same as Input. */
export function Select({ label, hint, error, id, className, children, ...props }: SelectProps) {
  const f = useField({ hint, error }, id);
  return (
    <Shell {...{ label, hint, error, className, ...f }}>
      <select
        id={f.controlId}
        aria-describedby={f.describedBy}
        aria-invalid={error ? true : undefined}
        className={control}
        {...props}
      >
        {children}
      </select>
    </Shell>
  );
}
