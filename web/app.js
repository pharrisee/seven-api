// ===== 7D2D API viewer: auth, htmx headers and disclosure persistence =====
// The page is server-rendered. This file only handles browser-local state.

(() => {
  const KEYS = {
    name: 'sdtd.tokenName',
    secret: 'sdtd.tokenSecret',
    cookie: 'sdtd.cookie',
  };

  const app = document.getElementById('app');
  const nameHeader = app?.dataset.nameHeader || 'X-SDTD-API-TOKENNAME';
  const secretHeader = app?.dataset.secretHeader || 'X-SDTD-API-SECRET';

  function readAuth() {
    return {
      name: localStorage.getItem(KEYS.name) || '',
      secret: localStorage.getItem(KEYS.secret) || '',
      cookie: localStorage.getItem(KEYS.cookie) || '',
    };
  }

  function hasAuth() {
    const auth = readAuth();
    return Boolean(auth.name || auth.secret || auth.cookie);
  }

  function updateAuthIndicator() {
    const button = document.getElementById('auth-btn');
    if (button) {
      button.style.borderColor = hasAuth() ? '#22c55e' : '#4b5563';
      button.title = hasAuth()
        ? 'Authentication configured (click to edit)'
        : 'Authentication settings';
    }
    document.querySelectorAll('[data-tryit-hint]').forEach((element) => {
      element.textContent = hasAuth()
        ? 'Using configured authentication'
        : 'No authentication configured (use 🔑 to add)';
    });
  }

  function openAuthDialog() {
    const dialog = document.getElementById('auth-dialog');
    if (!dialog) return;
    const auth = readAuth();
    const name = document.getElementById('auth-token-name');
    const secret = document.getElementById('auth-token-secret');
    const cookie = document.getElementById('auth-cookie');
    if (name) name.value = auth.name;
    if (secret) secret.value = auth.secret;
    if (cookie) cookie.value = auth.cookie;
    if (typeof dialog.showModal === 'function') {
      if (!dialog.open) dialog.showModal();
    } else {
      dialog.classList.remove('hidden');
    }
  }

  function closeAuthDialog() {
    const dialog = document.getElementById('auth-dialog');
    if (!dialog) return;
    if (typeof dialog.close === 'function') dialog.close();
    else dialog.classList.add('hidden');
  }

  function saveAuth() {
    const name = document.getElementById('auth-token-name');
    const secret = document.getElementById('auth-token-secret');
    const cookie = document.getElementById('auth-cookie');
    localStorage.setItem(KEYS.name, (name?.value || '').trim());
    localStorage.setItem(KEYS.secret, (secret?.value || '').trim());
    localStorage.setItem(KEYS.cookie, (cookie?.value || '').trim());
    closeAuthDialog();
    updateAuthIndicator();
  }

  function clearAuth() {
    localStorage.removeItem(KEYS.name);
    localStorage.removeItem(KEYS.secret);
    localStorage.removeItem(KEYS.cookie);
    for (const id of ['auth-token-name', 'auth-token-secret', 'auth-cookie']) {
      const element = document.getElementById(id);
      if (element) element.value = '';
    }
    updateAuthIndicator();
  }

  document.addEventListener('click', (event) => {
    if (event.target.closest('#auth-btn')) {
      event.preventDefault();
      openAuthDialog();
      return;
    }
    if (event.target.closest('#auth-close')) {
      event.preventDefault();
      closeAuthDialog();
      return;
    }
    if (event.target.closest('#auth-save')) {
      event.preventDefault();
      saveAuth();
      return;
    }
    if (event.target.closest('#auth-clear')) {
      event.preventDefault();
      clearAuth();
      return;
    }
    const dialog = event.target.closest('dialog#auth-dialog');
    if (dialog && event.target === dialog) {
      closeAuthDialog();
    }
  });

  // Add the browser-local credentials to every htmx request. The server turns
  // X-SDTD-SESSION into a Cookie header for the upstream API.
  document.addEventListener('htmx:configRequest', (event) => {
    const auth = readAuth();
    if (auth.name) event.detail.headers[nameHeader] = auth.name;
    if (auth.secret) event.detail.headers[secretHeader] = auth.secret;
    if (auth.cookie) event.detail.headers['X-SDTD-SESSION'] = auth.cookie;
  });

  // Persist <details> state in the sdtd_ui cookie via the server. The toggle
  // event does not bubble, so listen in the capture phase.
  const timers = new WeakMap();
  document.addEventListener('toggle', (event) => {
    const element = event.target;
    if (!(element instanceof HTMLDetailsElement)) return;
    const kind = element.dataset.stateKind;
    const name = element.dataset.stateName;
    if (!kind || !name || element.dataset.persist === '0') return;

    const previous = timers.get(element);
    if (previous) clearTimeout(previous);
    const timer = setTimeout(() => {
      fetch('/ui/toggle', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ kind, name, open: element.open }),
      }).catch(() => {
        // State persistence is best-effort.
      });
    }, 150);
    timers.set(element, timer);
  }, true);

  // ===== Operation summary popover =====
  // .op-summary is visually hidden inside each sidebar link. Show it in a
  // fixed-position popover so the narrow sidebar never clips or reflows.
  const opPopover = document.createElement('div');
  opPopover.id = 'op-popover';
  opPopover.className = 'op-popover';
  opPopover.setAttribute('role', 'tooltip');
  opPopover.hidden = true;
  document.body.appendChild(opPopover);

  let popoverLink = null;
  let pendingLink = null;
  let popoverTimer = null;

  function operationLinkFrom(event) {
    return event.target instanceof Element
      ? event.target.closest('a.operation-item')
      : null;
  }

  function hideOpPopover() {
    if (popoverTimer) {
      clearTimeout(popoverTimer);
      popoverTimer = null;
    }
    pendingLink = null;
    popoverLink = null;
    opPopover.hidden = true;
  }

  function positionOpPopover(link) {
    const gap = 8;
    const rect = link.getBoundingClientRect();
    const width = opPopover.offsetWidth;
    const height = opPopover.offsetHeight;
    const vw = window.innerWidth;
    const vh = window.innerHeight;

    let left = rect.right + gap;
    let top = rect.top;
    if (left + width > vw - gap) {
      if (rect.left - gap - width >= gap) {
        left = rect.left - gap - width;
      } else {
        left = Math.min(Math.max(gap, rect.left), vw - gap - width);
        top = rect.bottom + gap;
        if (top + height > vh - gap) top = rect.top - gap - height;
      }
    }
    if (top + height > vh - gap) top = vh - gap - height;
    if (top < gap) top = gap;

    opPopover.style.left = `${Math.max(gap, left)}px`;
    opPopover.style.top = `${top}px`;
  }

  function showOpPopover(link) {
    const summary = link.querySelector('.op-summary');
    const text = summary ? summary.textContent.trim() : '';
    if (!text) return;
    if (popoverTimer) {
      clearTimeout(popoverTimer);
      popoverTimer = null;
    }
    pendingLink = null;
    popoverLink = link;
    opPopover.textContent = text;
    opPopover.hidden = false;
    positionOpPopover(link);
  }

  function scheduleOpPopover(link) {
    if (link === popoverLink || link === pendingLink) return;
    hideOpPopover();
    pendingLink = link;
    popoverTimer = setTimeout(() => {
      popoverTimer = null;
      pendingLink = null;
      showOpPopover(link);
    }, 120);
  }

  document.addEventListener('pointerover', (event) => {
    const link = operationLinkFrom(event);
    if (link) scheduleOpPopover(link);
  });

  document.addEventListener('pointerout', (event) => {
    const link = operationLinkFrom(event);
    if (!link) return;
    if (event.relatedTarget && link.contains(event.relatedTarget)) return;
    if (link === popoverLink || link === pendingLink) hideOpPopover();
  });

  document.addEventListener('focusin', (event) => {
    const link = operationLinkFrom(event);
    if (link) showOpPopover(link);
  });

  document.addEventListener('focusout', (event) => {
    const link = operationLinkFrom(event);
    if (!link) return;
    if (event.relatedTarget && link.contains(event.relatedTarget)) return;
    if (link === popoverLink || link === pendingLink) hideOpPopover();
  });

  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') hideOpPopover();
  });

  document.addEventListener('click', (event) => {
    if (operationLinkFrom(event)) hideOpPopover();
  });

  document.addEventListener('htmx:beforeSwap', hideOpPopover);

  // The sidebar scrolls independently; a fixed popover would drift otherwise.
  window.addEventListener('scroll', hideOpPopover, true);
  window.addEventListener('resize', hideOpPopover);

  document.addEventListener('htmx:afterSwap', updateAuthIndicator);
  document.addEventListener('DOMContentLoaded', updateAuthIndicator);
  if (document.readyState !== 'loading') updateAuthIndicator();
})();
