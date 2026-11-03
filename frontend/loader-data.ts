// C04 browser-side loader-data access.
//
// The server delivers a CSR loader result two ways, both as JSON:
//   - initial load: a <script id="go-meta-csr-data" type="application/json">
//     element embedded in the boot shell (read once at bootstrap);
//   - soft navigation: the optional `data` field on the navigation envelope.
//
// This module normalizes both into a single typed read. The transport boundary
// is `unknown`; `getLoaderData<T>` narrows it to a page author's declared type
// without any runtime schema validation (none is implemented).
//
// Absence is distinct from a legitimate JSON null: `hasLoaderData` is false
// only when the server attached no payload at all (no registered loader).

const CSR_DATA_ELEMENT_ID = 'go-meta-csr-data';

/**
 * Read the initial-load loader payload embedded in the boot shell. Returns
 * `undefined` when the element is missing or empty (route has no loader), or
 * the parsed JSON value (which may be `null`) when a loader ran.
 *
 * The element is `type="application/json"`, so its text is inert — it is never
 * evaluated as JavaScript. The server HTML-escapes the JSON bytes, so a
 * `</script>` in the payload cannot close the element early.
 */
export function readInitialLoaderData(): unknown {
  if (typeof document === 'undefined') return undefined;
  const el = document.getElementById(CSR_DATA_ELEMENT_ID);
  if (!el) return undefined;
  const raw = el.textContent;
  if (raw == null || raw === '') return undefined;
  try {
    return JSON.parse(raw);
  } catch {
    // A malformed payload must not crash bootstrap; treat it as absent. The
    // server is the only writer and always emits valid JSON, so this is a
    // defensive path, not an expected one.
    return undefined;
  }
}

/** True when the server attached a loader payload (absent vs null distinct). */
export function hasLoaderData(value: unknown): value is NonNullable<unknown> {
  return value !== undefined;
}

/**
 * Narrow an `unknown` loader payload to a page author's declared type. No
 * runtime validation is performed — the page is responsible for the shape.
 * Returns `undefined` when the payload is absent so callers can fall back to
 * a no-data state.
 */
export function getLoaderData<T>(value: unknown): T | undefined {
  return value === undefined ? undefined : (value as T);
}
