import type { BadgeTone } from '@app/design';

import type { RequestStatus } from './api/client';

const units = ['B', 'KB', 'MB', 'GB', 'TB'];

/** A byte count for people: 1.5 GB, 700 KB, 12 B. */
export function formatBytes(bytes: number): string {
  let value = bytes;
  let unit = 0;
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000;
    unit++;
  }
  const digits = unit === 0 || value >= 100 ? 0 : 1;
  return `${value.toFixed(digits)} ${units[unit] ?? 'B'}`;
}

/** A date without the time, in the reader's locale. */
export function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

export const statusLabel: Record<RequestStatus, string> = {
  queued: 'Queued',
  downloading: 'Downloading',
  importing: 'Importing',
  imported: 'In library',
  failed: 'Failed',
};

export const statusTone: Record<RequestStatus, BadgeTone> = {
  queued: 'neutral',
  downloading: 'active',
  importing: 'active',
  imported: 'success',
  failed: 'danger',
};

/** Whether a request will change without anyone acting on it (worth polling for). */
export function isActive(status: RequestStatus): boolean {
  return status === 'queued' || status === 'downloading' || status === 'importing';
}
