// @dsCard group="Feedback"
import { Progress } from '../../src';

export function Partway() {
  return <Progress label="Downloading" value={0.42} className="max-w-sm" />;
}

export function Starting() {
  return <Progress label="Downloading" value={0} className="max-w-sm" />;
}

export function Done() {
  return <Progress label="Copying into the library" value={1} className="max-w-sm" />;
}
