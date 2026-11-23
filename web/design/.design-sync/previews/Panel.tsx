// @dsCard group="Layout"
import { Button, Panel } from '../../src';

export function Default() {
  return (
    <Panel className="max-w-md">
      <h2 className="font-medium">Groceries</h2>
      <p className="text-muted-foreground mt-1">Eggs, flour, and something for Sunday.</p>
    </Panel>
  );
}

export function WithHeading() {
  return (
    <Panel heading="Account" description="Signed in as robin@example.com." className="max-w-md">
      <Button>Sign out</Button>
    </Panel>
  );
}
