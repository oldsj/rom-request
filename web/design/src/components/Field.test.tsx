import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { Input, Select, Textarea } from './Field';

describe('Input', () => {
  it('is named by its label and described by its hint', () => {
    render(<Input label="Password" hint="At least 12 characters." />);
    const input = screen.getByLabelText('Password');
    expect(input).toHaveAccessibleDescription('At least 12 characters.');
    expect(input).not.toHaveAttribute('aria-invalid');
  });

  it('marks itself invalid and reads the error after the hint', () => {
    render(<Input label="Email" hint="We never share it." error="Enter an email address." />);
    const input = screen.getByLabelText('Email');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription('We never share it. Enter an email address.');
  });

  it('keeps a caller id', () => {
    render(<Textarea id="note-body" label="Body" />);
    expect(screen.getByLabelText('Body')).toHaveAttribute('id', 'note-body');
  });
});

describe('Select', () => {
  it('is named by its label and marks itself invalid', () => {
    render(
      <Select label="Platform" error="Choose one.">
        <option>GBA</option>
      </Select>,
    );
    const select = screen.getByLabelText('Platform');
    expect(select.tagName).toBe('SELECT');
    expect(select).toHaveAttribute('aria-invalid', 'true');
    expect(select).toHaveAccessibleDescription('Choose one.');
  });
});
