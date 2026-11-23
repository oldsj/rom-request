// @dsCard group="Feedback"
import { Notice } from '../../src';

export function Info() {
  return <Notice>No requests yet.</Notice>;
}

export function Error() {
  return <Notice tone="error">Wrong email or password.</Notice>;
}
