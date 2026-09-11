import '@/lib/api/transport';
import "@/style/global.css";
import App from "@/App.svelte";
import { mount } from "svelte";
import { initializeWorkspaceMedia } from '@/lib/api/workspace-media';
import { takeRecoveryTicket } from '@/lib/helper/recovery';

const initialRecoveryTicket = takeRecoveryTicket(window.location, window.history);
if (initialRecoveryTicket) window.dispatchEvent(new HashChangeEvent('hashchange'));
const app = Promise.race([
  initializeWorkspaceMedia(),
  new Promise<void>(resolve => window.setTimeout(resolve, 5000)),
]).then(() => mount(App, {
  target: document.body,
  props: { initialRecoveryTicket },
}));

export default app;
