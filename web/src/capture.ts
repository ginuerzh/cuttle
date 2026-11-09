import type { RequestCapture } from './api/types';

/** Reports whether a capture preview field was clipped to the configured limit. */
export function isTruncated(cap: RequestCapture | undefined, field: string): boolean {
  return cap?.truncatedFields?.includes(field) ?? false;
}
