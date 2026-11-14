// Fixture-only import map; the framework's generated registry is never patched.
export const pageLoaders: Record<string, () => Promise<{ default: unknown }>> = {
  'auth/recover/page.svelte': () => import('./Recovery.svelte'),
};
export const layoutLoaders: Record<string, () => Promise<{ default: unknown }>> = {};
export const routeLayouts: Record<string, string[]> = {};
