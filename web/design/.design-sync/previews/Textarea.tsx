// @dsCard group="Forms"
import { Textarea } from '../../src';

export function Default() {
  return <Textarea label="Body" placeholder="Write something" className="max-w-md" />;
}

export function WithError() {
  return <Textarea label="Body" defaultValue="" error="Write at least one line." className="max-w-md" />;
}
