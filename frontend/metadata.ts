import type { PageMetadata } from './types';

// Never place a scriptable URL into a canonical or social metadata tag, even
// if a malformed navigation response bypassed the normal server encoder.
function safeURL(value: string | undefined): string {
  if (!value || value !== value.trim() || /[\\\r\n\t]/.test(value)) return '';
  const relative = value.startsWith('/') && !value.startsWith('//');
  const absolute = /^https?:\/\//i.test(value);
  if (!relative && !absolute) return '';
  try {
    const parsed = new URL(value, window.location.origin);
    if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password) return '';
    return value;
  } catch {
    return '';
  }
}

// These fixed identifiers are not derived from metadata or used as selectors.
export function applyPageMetadata(meta?: PageMetadata): void {
  const fields: Array<{ key: string; attr: 'name' | 'property' | 'rel'; value: string | undefined }> = [
    { key: 'description', attr: 'name', value: meta?.description },
    { key: 'canonical', attr: 'rel', value: safeURL(meta?.canonical) },
    { key: 'og:title', attr: 'property', value: meta?.openGraph?.title },
    { key: 'og:description', attr: 'property', value: meta?.openGraph?.description },
    { key: 'og:image', attr: 'property', value: safeURL(meta?.openGraph?.image) },
    { key: 'og:url', attr: 'property', value: safeURL(meta?.openGraph?.url) },
    { key: 'og:type', attr: 'property', value: meta?.openGraph?.type },
    { key: 'og:site_name', attr: 'property', value: meta?.openGraph?.siteName },
    { key: 'twitter:card', attr: 'name', value: meta?.twitter?.card },
    { key: 'twitter:title', attr: 'name', value: meta?.twitter?.title },
    { key: 'twitter:description', attr: 'name', value: meta?.twitter?.description },
    { key: 'twitter:image', attr: 'name', value: safeURL(meta?.twitter?.image) },
    { key: 'twitter:site', attr: 'name', value: meta?.twitter?.site },
  ];
  const owned = Array.from(document.head.querySelectorAll('[data-meta-managed]'));
  for (const { key, attr, value } of fields) {
    const existing = owned.filter((node) => node.getAttribute('data-meta-managed') === key);
    if (!value) {
      existing.forEach((node) => node.remove());
      continue;
    }
    const tagName = key === 'canonical' ? 'LINK' : 'META';
    let element = existing.find((node) => node.tagName === tagName);
    existing.filter((node) => node !== element).forEach((node) => node.remove());
    if (!element) {
      element = document.createElement(tagName.toLowerCase());
      element.setAttribute('data-meta-managed', key);
      document.head.appendChild(element);
    }
    element.setAttribute(attr, key);
    element.setAttribute(key === 'canonical' ? 'href' : 'content', value);
  }
}
