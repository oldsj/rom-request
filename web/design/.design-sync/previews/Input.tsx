// @dsCard group="Forms"
import { Input } from '../../src';

export function Default() {
  return <Input label="Email" name="email" type="email" autoComplete="off" className="max-w-sm" />;
}

export function WithHint() {
  return <Input label="Password" type="password" hint="At least 12 characters." autoComplete="off" className="max-w-sm" />;
}

export function WithError() {
  return (
    <Input
      label="Email"
      type="email"
      defaultValue="robin@example"
      error="Enter an email address like name@example.com."
      autoComplete="off"
      className="max-w-sm"
    />
  );
}

export function Disabled() {
  return <Input label="Username" defaultValue="robin" disabled className="max-w-sm" />;
}
