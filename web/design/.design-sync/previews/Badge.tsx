// @dsCard group="Feedback"
import { Badge } from '../../src';

export function Tones() {
  return (
    <div className="flex flex-wrap gap-2">
      <Badge>Queued</Badge>
      <Badge tone="active">Downloading</Badge>
      <Badge tone="success">Imported</Badge>
      <Badge tone="danger">Failed</Badge>
    </div>
  );
}
