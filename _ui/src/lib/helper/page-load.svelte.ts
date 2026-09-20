import { isFeatureEnabled, loadFeatures } from '../store/features.svelte';
import { authErrorMessage } from '../api/auth';

export interface LoadIssue { label: string; message: string; disabled: boolean }

/** Each resource commits independently. A failed read is never an empty success. */
export function createPageLoader() {
  const state = $state({ issues: [] as LoadIssue[], pending: [] as string[] });
  let generation = 0;
  return {
    get issues() { return state.issues; },
    reset() { generation++; state.issues = []; state.pending = []; },
    loading(label: string) { return state.pending.includes(label); },
    error(label: string) { return state.issues.find(issue => issue.label === label)?.message || ''; },
    async load<T>(label: string, request: () => Promise<T>, apply: (value: T) => void, feature?: string): Promise<boolean> {
      const run = generation;
      state.pending = [...state.pending, label];
      let issue: LoadIssue | undefined;
      try {
        if (feature) {
          // On a catalog failure, let the real endpoint decide admission.
          await loadFeatures().catch(() => {});
          if (!isFeatureEnabled(feature)) {
            issue = { label, message: `${label} is unavailable because its feature is disabled.`, disabled: true };
          }
        }
        if (!issue) {
          const value = await request();
          if (run !== generation) return false;
          apply(value);
          state.issues = state.issues.filter(item => item.label !== label);
          return true;
        }
      } catch (error: unknown) {
        const response = (error as { response?: { status?: number; data?: { code?: string } } })?.response;
        const disabled = response?.data?.code === 'feature_disabled';
        const message = disabled ? `${label} is unavailable because its feature is disabled.`
          : response?.status === 403 ? `You do not have access to ${label.toLowerCase()}.`
          : response?.status === 404 ? `${label} could not be loaded: the endpoint is unavailable. This is not an empty list.`
          : `${label}: ${authErrorMessage(error, 'Could not load data. Please retry.')}`;
        issue = { label, message, disabled };
      } finally {
        if (run === generation) state.pending = state.pending.filter(item => item !== label);
      }
      if (run === generation && issue) state.issues = [...state.issues.filter(item => item.label !== label), issue];
      return false;
    },
  };
}
