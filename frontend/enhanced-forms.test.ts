import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { installEnhancedForms } from './enhanced-forms';

function form(marked = true): HTMLFormElement {
  const f = document.createElement('form');
  f.method = 'post'; f.enctype = 'application/x-www-form-urlencoded'; f.action = '/_meta/actions/test.action';
  if (marked) f.setAttribute('data-go-meta-enhance', '');
  f.innerHTML = '<input name="_csrf" value="signed"><input name="name" value="hello"><input name="tags" value="one"><input name="tags" value="two">';
  document.body.append(f);
  return f;
}
function submit(f: HTMLFormElement): SubmitEvent {
  const event = new SubmitEvent('submit', { bubbles: true, cancelable: true });
  f.dispatchEvent(event); return event;
}
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('enhanced forms', () => {
  beforeEach(() => { window.location.href = 'http://localhost/'; installEnhancedForms(); });
  afterEach(() => { document.body.replaceChildren(); vi.unstubAllGlobals(); });
  it('leaves unmarked, cross-origin and unsupported forms native', () => {
    const f = form(false); expect(submit(f).defaultPrevented).toBe(false);
    f.setAttribute('data-go-meta-enhance', ''); f.action = 'https://foreign.test/_meta/actions/a'; expect(submit(f).defaultPrevented).toBe(false);
    f.action = '/_meta/actions/a'; f.enctype = 'multipart/form-data'; expect(submit(f).defaultPrevented).toBe(false);
    f.enctype = 'application/x-www-form-urlencoded';
    const base = document.createElement('base'); base.target = '_blank'; document.head.append(base);
    expect(submit(f).defaultPrevented).toBe(false); base.remove();
  });
  it('preserves repeated values and csrf and suppresses duplicate requests', async () => {
    let finish!: (v: Response) => void;
    const fetcher = vi.fn((_url: URL, _opts: RequestInit) => new Promise<Response>((resolve) => { finish = resolve; })); vi.stubGlobal('fetch', fetcher);
    const f = form(); expect(submit(f).defaultPrevented).toBe(true); expect(submit(f).defaultPrevented).toBe(true);
    expect(fetcher).toHaveBeenCalledTimes(1); expect(f.getAttribute('aria-busy')).toBe('true');
    const opts = fetcher.mock.calls[0][1] as unknown as RequestInit;
    const values = opts.body as URLSearchParams;
    expect(values.get('_csrf')).toBe('signed'); expect(values.getAll('tags')).toEqual(['one', 'two']);
    finish(new Response(JSON.stringify({ ok: false, error: 'validation failed', message: 'Fix it', errors: { name: ['<bad>', 'again'] } }), { status: 422, headers: { 'Content-Type': 'application/json' } }));
    await flush();
    const input = f.elements.namedItem('name') as HTMLInputElement;
    expect(input.getAttribute('aria-invalid')).toBe('true');
    const error = document.getElementById(input.getAttribute('aria-describedby')!);
    expect(error?.textContent).toBe('<bad>again'); expect(error?.querySelector('bad')).toBeNull();
    expect(f.textContent).toContain('Fix it'); expect(f.hasAttribute('aria-busy')).toBe(false);
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('offline'))));
    submit(f); await flush();
    expect(input.hasAttribute('aria-invalid')).toBe(false);
    expect(f.textContent).toContain('outcome is unknown');
  });
  it('keeps errors within the submitted form and ignores removed forms', async () => {
    let finish!: (v: Response) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { finish = resolve; })));
    const first = form(), second = form(); submit(first); first.remove(); await flush();
    finish(new Response(JSON.stringify({ ok: false, error: 'validation failed', errors: { name: ['wrong'] } }), { status: 422, headers: { 'Content-Type': 'application/json' } }));
    await flush(); expect(second.textContent).not.toContain('wrong'); expect(first.textContent).not.toContain('wrong');
  });
});
