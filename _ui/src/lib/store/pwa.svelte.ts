interface InstallPromptEvent extends Event {
  prompt(): Promise<void>;
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>;
}

export const pwa = $state({
  prompt: null as InstallPromptEvent | null,
  installed: false,
  offline: false,
});

export function initializePWA() {
  const standalone = window.matchMedia('(display-mode: standalone)');
  const syncDisplay = () => { pwa.installed = standalone.matches || (navigator as Navigator & { standalone?: boolean }).standalone === true; };
  syncDisplay();
  standalone.addEventListener('change', syncDisplay);
  pwa.offline = !navigator.onLine;
  window.addEventListener('offline', () => { pwa.offline = true; });
  window.addEventListener('online', () => { pwa.offline = false; });
  window.addEventListener('beforeinstallprompt', event => {
    event.preventDefault();
    pwa.prompt = event as InstallPromptEvent;
  });
  window.addEventListener('appinstalled', () => { pwa.installed = true; pwa.prompt = null; });
}
