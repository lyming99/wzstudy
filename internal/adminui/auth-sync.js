'use strict';

// Broadcast only an anonymous change signal, never an identity or credential.
// Receivers re-check the server before keeping sensitive views visible.
(() => {
  const key = 'wzstudy-auth-change';
  const listeners = new Set();
  let channel = null;
  try {
    if ('BroadcastChannel' in window) {
      channel = new BroadcastChannel(key);
      channel.onmessage = () => listeners.forEach(listener => listener());
    }
  } catch (_) { channel = null; }
  if (!channel) {
    window.addEventListener('storage', event => {
      if (event.key === key && event.newValue) listeners.forEach(listener => listener());
    });
  }
  window.wzAuthSync = Object.freeze({
    subscribe(listener) { listeners.add(listener); },
    notify() {
      if (channel) { channel.postMessage('changed'); return; }
      try {
        localStorage.setItem(key, String(Date.now()) + Math.random());
        localStorage.removeItem(key);
      } catch (_) { /* Focus checks still revalidate if storage is unavailable. */ }
    }
  });
})();
