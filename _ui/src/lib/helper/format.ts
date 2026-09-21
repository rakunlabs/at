export function formatDate(date: string | Date | undefined | null, includeTime = false): string {
  if (!date) return 'N/A';
  
  const d = typeof date === 'string' ? new Date(date) : date;
  
  if (isNaN(d.getTime())) return 'Invalid Date';

  if (includeTime) return d.toISOString();

  const options: Intl.DateTimeFormatOptions = {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  };

  return d.toLocaleDateString('en-US', options);
}

export function formatDateTime(date: string | Date | undefined | null): string {
  return formatDate(date, true);
}

export function formatTime(date: string | Date | undefined | null): string {
  if (!date) return '-';
  const d = typeof date === 'string' ? new Date(date) : date;
  if (isNaN(d.getTime())) return 'Invalid Date';
  return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

/**
 * A transcript stamp: the clock time alone while the entry is from today, and
 * the date in front of it once it is not.
 *
 * The date is dropped for today because a transcript is read top to bottom and
 * repeating it on every bubble is noise; it is added as soon as the entry is
 * older, because a bare `09:14` on a conversation resumed days later is
 * actively misleading. The year appears only across a year boundary.
 *
 * Returns `''` — not a placeholder — when there is nothing to show: a message
 * still in flight has no timestamp yet, and `N/A` in a transcript reads as a
 * failure rather than as an absence. The browser locale is used deliberately;
 * this is a wall-clock reading for the person looking at it.
 */
export function formatMessageTime(date: string | Date | undefined | null): string {
  if (!date) return '';
  const d = typeof date === 'string' ? new Date(date) : date;
  if (isNaN(d.getTime())) return '';

  const now = new Date();
  const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  if (d.toDateString() === now.toDateString()) return time;

  const day = d.toLocaleDateString(
    undefined,
    d.getFullYear() === now.getFullYear()
      ? { month: 'short', day: 'numeric' }
      : { year: 'numeric', month: 'short', day: 'numeric' },
  );

  return `${day} ${time}`;
}

/** The full localized stamp, for the `title` behind an abbreviated one. */
export function formatLocalDateTime(date: string | Date | undefined | null): string {
  if (!date) return '';
  const d = typeof date === 'string' ? new Date(date) : date;
  if (isNaN(d.getTime())) return '';
  return d.toLocaleString();
}
