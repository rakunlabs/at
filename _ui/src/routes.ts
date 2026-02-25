import Home from '@/pages/Home.svelte';
import Providers from '@/pages/Providers.svelte';
import Chat from '@/pages/Chat.svelte';
import Tokens from '@/pages/Tokens.svelte';
import Workflows from '@/pages/Workflows.svelte';
import WorkflowEditor from '@/pages/WorkflowEditor.svelte';
import Docs from '@/pages/Docs.svelte';
import Settings from '@/pages/Settings.svelte';
import NotFound from '@/pages/NotFound.svelte';

export default {
  '/': Home,
  '/providers': Providers,
  '/chat': Chat,
  '/tokens': Tokens,
  '/workflows': Workflows,
  '/workflows/:id': WorkflowEditor,
  '/docs': Docs,
  '/settings': Settings,
  '*': NotFound
};
