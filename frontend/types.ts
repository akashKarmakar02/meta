// Navigation protocol types shared by the client runtime (Phase F).
//
// This module mirrors internal/httpserver/response.go field-for-field:
// the wire envelope is defined once as Go structs here as TypeScript
// types there, and both sides are pinned to the same checked-in JSON
// fixtures (tests/fixtures/*.json) so the contract cannot drift.
// See blueprint section 5.1 for the protocol description.

/** Protocol version this build speaks. Unsupported versions trigger full page navigation. */
export const NAVIGATION_VERSION = 1;

/** Request header that marks a GET as a soft-navigation request. */
export const NAVIGATION_HEADER = 'X-Go-Web-Navigation';

/** Rendering mode of the target route. */
export type NavigationMode = 'ssr' | 'csr';

/**
 * Soft-navigation JSON envelope (blueprint 5.1).
 *
 * - `html` carries server-owned templ output only; never executable content.
 * - `clientEntry` maps to the generated static import registry, never an
 *   evaluated path; null for SSR routes.
 * - `layouts` contains zero or one stable visual-layout ID.
 * - `html` includes the server-owned visual layout when one is selected,
 *   including the mount element for a Svelte page in a templ layout.
 */
export interface NavigationResponse {
  version: number;
  url: string;
  routeId: string;
  mode: NavigationMode;
  title: string;
  layouts: string[];
  html: string;
  clientEntry: string | null;
  // C04: optional CSR loader payload. Absent when the route has no registered
  // loader; present (possibly JSON null) when a loader ran. The transport
  // boundary is `unknown` — page authors narrow it to their own type.
  data?: unknown;
}
