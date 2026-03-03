import type { SortEntry } from '@/lib/components/SortableHeader.svelte';

/**
 * Toggle sort for a field. If multiSort is true (shift+click),
 * the field is added/toggled/removed from the sort list.
 * Otherwise it replaces all existing sorts.
 *
 * Cycle: none → asc → desc → none
 */
export function toggleSort(sorts: SortEntry[], field: string, multiSort: boolean): SortEntry[] {
  const existing = sorts.find(s => s.field === field);

  if (multiSort) {
    if (!existing) {
      return [...sorts, { field, desc: false }];
    } else if (!existing.desc) {
      return sorts.map(s => s.field === field ? { ...s, desc: true } : s);
    } else {
      return sorts.filter(s => s.field !== field);
    }
  } else {
    if (!existing) {
      return [{ field, desc: false }];
    } else if (!existing.desc) {
      return [{ field, desc: true }];
    } else {
      return [];
    }
  }
}

/**
 * Build `_sort` query param string from sort entries.
 * Format: "-field1,+field2" (prefix - for desc, + for asc)
 */
export function buildSortParam(sorts: SortEntry[]): string {
  if (sorts.length === 0) return '';
  return sorts.map(s => (s.desc ? '-' : '+') + s.field).join(',');
}
