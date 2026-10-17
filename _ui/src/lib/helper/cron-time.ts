/** Keep the occurrence's offset: it is the scheduler's timezone, not the device's. */
export function cronRunTime(value: string, timezone?: string): string {
  if (timezone) {
    try {
      return new Intl.DateTimeFormat('sv-SE', {
        timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit',
        hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZoneName: 'shortOffset',
      }).format(new Date(value));
    } catch { /* Unknown zone: retain the explicit timestamp offset. */ }
  }
  return value.replace('T', ' ').replace(/:00(?=Z|[+-]\d{2}:\d{2}$)/, '').replace('Z', ' UTC');
}

export function cronRemaining(value: string, now: number): string {
  const delta = Date.parse(value) - now;
  if (!Number.isFinite(delta)) return 'Unavailable';
  if (delta <= 0) return 'Refreshing…';
  if (delta < 60000) return 'Less than a minute left';
  const minutes = Math.ceil(delta / 60000);
  if (minutes < 60) return `${minutes}m left`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m left`;
  return `${Math.floor(hours / 24)}d ${hours % 24}h left`;
}
