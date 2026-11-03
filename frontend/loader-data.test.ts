// C04 client tests: the browser-side loader-data transport boundary.
// Covers the initial-load element read, absent-vs-null distinction, typed
// narrowing, and the mount-time prop hand-off.
import { describe, it, expect, beforeEach } from 'vitest';
import {
  readInitialLoaderData,
  hasLoaderData,
  getLoaderData,
} from './loader-data';

function installDataElement(text: string | null): void {
  document.body.innerHTML =
    text === null
      ? '<div id="go-meta-csr-data"></div>'
      : `<script id="go-meta-csr-data" type="application/json">${text}</script>`;
}

describe('readInitialLoaderData (C04)', () => {
  beforeEach(() => {
    document.body.innerHTML = '';
  });

  it('returns undefined when no data element is present', () => {
    expect(readInitialLoaderData()).toBeUndefined();
  });

  it('returns undefined for an empty element (no loader)', () => {
    installDataElement(null);
    expect(readInitialLoaderData()).toBeUndefined();
  });

  it('parses a JSON object payload', () => {
    installDataElement('{"teamId":"team-42","greeting":"Loaded by Go"}');
    expect(readInitialLoaderData()).toEqual({
      teamId: 'team-42',
      greeting: 'Loaded by Go',
    });
  });

  it('distinguishes an explicit JSON null from absence', () => {
    installDataElement('null');
    const value = readInitialLoaderData();
    expect(value).toBeNull();
    expect(hasLoaderData(value)).toBe(true); // present, but null
  });

  it('parses primitive and array payloads', () => {
    installDataElement('42');
    expect(readInitialLoaderData()).toBe(42);
    installDataElement('[1,2,3]');
    expect(readInitialLoaderData()).toEqual([1, 2, 3]);
  });

  it('treats a malformed payload as absent (does not throw)', () => {
    installDataElement('{not json');
    expect(readInitialLoaderData()).toBeUndefined();
  });

  it('round-trips a script-breaking payload in the server-escaped form', () => {
    // json.Marshal HTML-escapes <, >, & to < > &, so the element
    // content contains no literal angle brackets and cannot close the script
    // element. This asserts the client read round-trips the original value.
    installDataElement('{"x":"a\\u003c/script\\u003e\\u003cscript\\u003ealert(1)\\u003c/script\\u003e"}');
    const value = readInitialLoaderData() as { x: string };
    expect(value.x).toBe('a</script><script>alert(1)</script>');
  });
});

describe('hasLoaderData / getLoaderData (C04)', () => {
  it('hasLoaderData is false only for absence', () => {
    expect(hasLoaderData(undefined)).toBe(false);
    expect(hasLoaderData(null)).toBe(true);
    expect(hasLoaderData({})).toBe(true);
  });

  it('getLoaderData returns undefined for absence', () => {
    expect(getLoaderData<number | undefined>(undefined)).toBeUndefined();
  });

  it('getLoaderData narrows an object payload to the declared type', () => {
    const data = getLoaderData<{ teamId: string }>({ teamId: 'team-1' });
    expect(data?.teamId).toBe('team-1');
  });

  it('getLoaderData preserves a legitimate null', () => {
    const data = getLoaderData<null | undefined>(null);
    expect(data).toBeNull();
  });
});
