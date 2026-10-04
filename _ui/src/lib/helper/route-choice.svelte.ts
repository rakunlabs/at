import { router } from 'svelte-spa-router';
import { updateRouteQuery } from './route-query';

/** URL-owned selection: Back/Forward and direct links use the same source of truth. */
export function routeChoice<T extends string>(key: string, choices: readonly T[], fallback: T) {
  return {
    get value(): T {
      const value = new URLSearchParams(router.querystring || '').get(key) as T;
      return choices.includes(value) ? value : fallback;
    },
    set value(value: T) {
      updateRouteQuery({ [key]: value === fallback ? null : value });
    },
  };
}
