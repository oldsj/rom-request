// @dsCard group="Forms"
import { Select } from '../../src';

export function Default() {
  return (
    <Select label="Platform" defaultValue="" className="max-w-sm">
      <option value="" disabled>Choose a platform</option>
      <option>GBA</option>
      <option>PS1</option>
    </Select>
  );
}

export function WithError() {
  return (
    <Select label="Platform" error="Choose a platform folder from the library." className="max-w-sm">
      <option>GBA</option>
    </Select>
  );
}
