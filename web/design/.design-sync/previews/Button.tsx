// @dsCard group="Actions"
import { Button } from '../../src';

export function Variants() {
  return (
    <div className="flex flex-wrap gap-3">
      <Button variant="primary">Save changes</Button>
      <Button>Cancel</Button>
      <Button variant="danger">Delete note</Button>
    </div>
  );
}

export function Disabled() {
  return (
    <div className="flex flex-wrap gap-3">
      <Button variant="primary" disabled>
        Save changes
      </Button>
      <Button disabled>Cancel</Button>
    </div>
  );
}

export function FullWidth() {
  return (
    <div className="max-w-xs">
      <Button variant="primary" fullWidth>
        Sign in
      </Button>
    </div>
  );
}
