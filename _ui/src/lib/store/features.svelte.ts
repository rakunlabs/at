import { listFeatures, type Feature, type FeatureGroup } from '@/lib/api/features';

export const storeFeatures = $state({
  loaded: false,
  loading: false,
  groups: [] as FeatureGroup[],
  features: [] as Feature[],
  flags: {} as Record<string, boolean>,
});

let loadPromise: Promise<void> | null = null;

export function isFeatureEnabled(key: string): boolean {
  if (!storeFeatures.loaded) return true;
  return storeFeatures.flags[key] !== false;
}

export function applyFeature(feature: Feature) {
  storeFeatures.flags[feature.key] = feature.enabled;
  storeFeatures.features = storeFeatures.features.map((item) => (item.key === feature.key ? feature : item));
  storeFeatures.groups = storeFeatures.groups.map((group) => ({
    ...group,
    features: group.features.map((item) => (item.key === feature.key ? feature : item)),
  }));
}

export async function loadFeatures(force = false): Promise<void> {
  if (storeFeatures.loaded && !force) return;
  if (loadPromise && !force) return loadPromise;

  storeFeatures.loading = true;
  loadPromise = listFeatures()
    .then((res) => {
      storeFeatures.groups = res.groups || [];
      storeFeatures.features = res.features || [];
      const flags: Record<string, boolean> = {};
      for (const feature of storeFeatures.features) {
        flags[feature.key] = feature.enabled;
      }
      storeFeatures.flags = flags;
      storeFeatures.loaded = true;
    })
    .finally(() => {
      storeFeatures.loading = false;
      loadPromise = null;
    });

  return loadPromise;
}
