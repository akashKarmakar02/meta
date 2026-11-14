import { subscribeNavigation } from './navigation-state';

/** Optional, indeterminate UI outside all page/layout outlets. Returns complete cleanup. */
export function installNavigationProgress(): () => void {
  const host = document.createElement('div');
  host.setAttribute('data-meta-navigation-progress', '');
  // Shadow styles work in copied starter runtimes without a CSS build entry.
  const shadow = host.attachShadow({ mode: 'open' });
  shadow.innerHTML = `<style>
    :host { position:fixed; inset:0 0 auto; z-index:2147483647; pointer-events:none; }
    .track { height:3px; overflow:hidden; background:color-mix(in srgb, #2563eb 15%, transparent); }
    .bar { width:35%; height:100%; background:#2563eb; animation:meta-pending 1s ease-in-out infinite; }
    .status { position:absolute; width:1px; height:1px; overflow:hidden; clip-path:inset(50%); }
    @keyframes meta-pending { from { transform:translateX(-100%); } to { transform:translateX(386%); } }
    @media (prefers-reduced-motion:reduce) { .bar { animation:none; width:100%; } }
  </style><div class="track" aria-hidden="true"><div class="bar"></div></div><span class="status" role="status" aria-live="polite"></span>`;
  const status = shadow.querySelector('.status')!;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const clear = () => { clearTimeout(timer); timer = undefined; host.remove(); status.textContent = ''; };
  const unsubscribe = subscribeNavigation((state) => {
    clear();
    if (state.status !== 'pending') return;
    timer = setTimeout(() => {
      timer = undefined;
      document.body.appendChild(host);
      status.textContent = 'Loading page';
    }, 150);
  });
  return () => { unsubscribe(); clear(); };
}
