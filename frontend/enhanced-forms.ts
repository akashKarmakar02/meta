import { currentNavigationId } from './navigation-coordinator';

const active = new Map<HTMLFormElement, AbortController>();
const feedback = new WeakMap<HTMLFormElement, { nodes: Element[]; controls: Array<{ element: HTMLElement; invalid: string | null; id: string }> }>();
let installed = false;
let sequence = 0;
let departureGeneration = 0;

function submitterSupported(): boolean {
  const form = document.createElement('form');
  const button = document.createElement('button');
  button.name = '__go_meta_probe';
  button.value = 'yes';
  form.append(button);
  try { return new FormData(form, button).get(button.name) === 'yes'; } catch { return false; }
}

function eligible(form: HTMLFormElement, submitter: HTMLElement | null): URL | null {
  if (!form.hasAttribute('data-go-meta-enhance')) return null;
  const button = submitter instanceof HTMLButtonElement || submitter instanceof HTMLInputElement ? submitter : null;
  if (button instanceof HTMLInputElement && button.type.toLowerCase() === 'image') return null;
  const method = button?.hasAttribute('formmethod') ? button.formMethod : (form.getAttribute('method') ?? 'get');
  const encoding = button?.hasAttribute('formenctype') ? button.formEnctype : (form.getAttribute('enctype') ?? 'application/x-www-form-urlencoded');
  const target = button?.hasAttribute('formtarget') ? button.formTarget :
    (form.getAttribute('target') ?? document.querySelector('base[target]')?.getAttribute('target') ?? '');
  if (method.toLowerCase() !== 'post' || encoding.toLowerCase() !== 'application/x-www-form-urlencoded' || (target && target.toLowerCase() !== '_self')) return null;
  try {
    const url = new URL(button?.hasAttribute('formaction') ? button.formAction : (form.getAttribute('action') || document.URL), document.baseURI);
    if (url.origin !== window.location.origin || !/^\/_meta\/actions\/[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,126}[A-Za-z0-9])?$/.test(url.pathname) || url.hash) return null;
    return url;
  } catch { return null; }
}

function clearFeedback(form: HTMLFormElement): void {
  const old = feedback.get(form);
  if (!old) return;
  for (const node of old.nodes) node.remove();
  for (const { element, invalid, id } of old.controls) {
    const rest = (element.getAttribute('aria-describedby') ?? '').split(/\s+/).filter((s) => s && s !== id);
    if (rest.length) element.setAttribute('aria-describedby', rest.join(' '));
    else element.removeAttribute('aria-describedby');
    if (invalid === null) element.removeAttribute('aria-invalid');
    else element.setAttribute('aria-invalid', invalid);
  }
  feedback.delete(form);
}

function showFeedback(form: HTMLFormElement, message: string, fields: Record<string, string[]> = {}): void {
  clearFeedback(form);
  const state: { nodes: Element[]; controls: Array<{ element: HTMLElement; invalid: string | null; id: string }> } = { nodes: [], controls: [] };
  feedback.set(form, state);
  if (message) {
    const summary = document.createElement('p');
    summary.setAttribute('role', 'alert');
    summary.textContent = message;
    form.prepend(summary);
    state.nodes.push(summary);
  }
  for (const [name, messages] of Object.entries(fields)) {
    const controls = Array.from(form.elements).filter((item): item is HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement =>
      (item instanceof HTMLInputElement || item instanceof HTMLTextAreaElement || item instanceof HTMLSelectElement) && item.name === name && item.type !== 'hidden');
    if (!controls.length || !messages.length) continue;
    const error = document.createElement('div');
    do { error.id = `go-meta-form-error-${++sequence}`; } while (document.getElementById(error.id));
    for (const message of messages) {
      const paragraph = document.createElement('p');
      paragraph.textContent = message;
      error.append(paragraph);
    }
    controls[controls.length - 1].insertAdjacentElement('afterend', error);
    state.nodes.push(error);
    for (const control of controls) {
      state.controls.push({ element: control, invalid: control.getAttribute('aria-invalid'), id: error.id });
      control.setAttribute('aria-invalid', 'true');
      control.setAttribute('aria-describedby', [control.getAttribute('aria-describedby'), error.id].filter(Boolean).join(' '));
    }
  }
}

function validation(value: unknown): { message: string; errors: Record<string, string[]> } | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const obj = value as Record<string, unknown>;
  if (obj.ok !== false || obj.error !== 'validation failed' || (obj.message !== undefined && typeof obj.message !== 'string') || !obj.errors || typeof obj.errors !== 'object' || Array.isArray(obj.errors)) return null;
  const fields = obj.errors as Record<string, unknown>;
  if (Object.values(fields).some((messages) => !Array.isArray(messages) || messages.some((m) => typeof m !== 'string'))) return null;
  return { message: (obj.message as string | undefined) ?? '', errors: fields as Record<string, string[]> };
}

function safeDestination(value: unknown): string | null {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || value.includes('\\') || /[\u0000-\u001f\u007f]/.test(value)) return null;
  try { const url = new URL(value, location.origin); return url.origin === location.origin ? url.href : null; } catch { return null; }
}

function onSubmit(event: SubmitEvent): void {
  const form = event.target;
  if (!(form instanceof HTMLFormElement) || event.defaultPrevented) return;
  if (active.has(form)) { event.preventDefault(); return; }
  const submitter = event.submitter;
  const url = eligible(form, submitter);
  if (!url || (submitter && !submitterSupported())) return;
  let data: FormData;
  try { data = submitter ? new FormData(form, submitter) : new FormData(form); } catch { return; }
  const params = new URLSearchParams();
  for (const [key, value] of data) {
    if (typeof value !== 'string') return; // Native form semantics remain authoritative for files.
    // The HTML URL-encoded submission algorithm normalizes newlines to CRLF.
    const normalize = (text: string) => text.replace(/\r\n|\r|\n/g, '\r\n');
    params.append(normalize(key), normalize(value));
  }
  event.preventDefault();
  clearFeedback(form);
  const controller = new AbortController();
  const navId = currentNavigationId();
  const generation = departureGeneration;
  const busy = form.getAttribute('aria-busy');
  const owns = () => form.isConnected && !controller.signal.aborted && navId === currentNavigationId() && generation === departureGeneration;
  active.set(form, controller);
  form.setAttribute('aria-busy', 'true');
  form.setAttribute('data-go-meta-pending', '');
  void (async () => {
    try {
      const response = await fetch(url, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/x-www-form-urlencoded;charset=UTF-8', 'Accept': 'application/json', 'X-Go-Meta-Action': '1' }, body: params, signal: controller.signal, redirect: 'error' });
      if (!owns()) return;
      const type = response.headers.get('content-type')?.split(';')[0].trim().toLowerCase();
      const payload: unknown = type === 'application/json' ? await response.json() : null;
      if (!owns()) return;
      if (response.status === 422) {
        const details = validation(payload);
        if (details) { showFeedback(form, details.message || 'Please correct the highlighted fields.', details.errors); return; }
      }
      if (response.ok && payload && typeof payload === 'object' && !Array.isArray(payload)) {
        const result = payload as Record<string, unknown>;
        const destination = result.ok === true ? safeDestination(result.redirect) : null;
        if (destination) {
          departureGeneration++;
          for (const other of active.values()) if (other !== controller) other.abort();
          showFeedback(form, 'Submission accepted. Continue to the destination if navigation does not start.');
          const link = document.createElement('a');
          link.href = destination;
          link.textContent = 'Continue';
          form.append(link);
          feedback.get(form)?.nodes.push(link);
          try { window.location.assign(destination); } catch { /* The accepted-result link remains available. */ }
          return;
        }
      }
      showFeedback(form, 'The submission could not be confirmed. Check its result before trying again.');
    } catch {
      if (owns()) showFeedback(form, 'The submission outcome is unknown. Check its result before trying again.');
    } finally {
      if (active.get(form) === controller) {
        active.delete(form);
        if (busy === null) form.removeAttribute('aria-busy'); else form.setAttribute('aria-busy', busy);
        form.removeAttribute('data-go-meta-pending');
      }
    }
  })();
}

/** Install one delegated handler for both SSR and client-mounted forms. */
export function installEnhancedForms(): void {
  if (installed) return;
  installed = true;
  document.addEventListener('submit', onSubmit);
  const observer = new MutationObserver(() => {
    for (const [form, controller] of active) if (!form.isConnected) controller.abort();
  });
  observer.observe(document.documentElement, { childList: true, subtree: true });
  window.addEventListener('pagehide', () => { for (const controller of active.values()) controller.abort(); });
}
