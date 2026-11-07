import { afterEach, describe, expect, it } from 'vitest';
import { applyPageMetadata } from './metadata';

afterEach(() => {
  document.head.querySelectorAll('[data-meta-managed], [data-test-unmanaged]').forEach((node) => node.remove());
});

describe('page metadata', () => {
  it('updates and removes owned canonical and social tags without touching other tags', () => {
    const external = document.createElement('meta');
    external.setAttribute('name', 'author');
    external.setAttribute('data-test-unmanaged', '');
    document.head.appendChild(external);
    applyPageMetadata({
      canonical: 'https://example.com/post?a=1&b=2',
      openGraph: { title: '<Example>', image: '/images/hero.jpg' },
      twitter: { card: 'summary_large_image', image: '/images/hero.jpg' },
    });
    expect(document.head.querySelector('link[rel="canonical"]')?.getAttribute('href')).toBe('https://example.com/post?a=1&b=2');
    expect(document.head.querySelector('meta[property="og:title"]')?.getAttribute('content')).toBe('<Example>');
    expect(document.head.querySelector('meta[name="twitter:card"]')?.getAttribute('content')).toBe('summary_large_image');
    applyPageMetadata({ canonical: 'javascript:alert(1)' });
    expect(document.head.querySelector('link[rel="canonical"]')).toBeNull();
    expect(document.head.querySelector('meta[property="og:title"]')).toBeNull();
    expect(document.head.querySelector('[data-test-unmanaged]')).toBe(external);
  });
});
