import Home from '@/pages/Home.svelte';
import Providers from '@/pages/Providers.svelte';
import Skills from '@/pages/Skills.svelte';
import Chat from '@/pages/Chat.svelte';
import Tokens from '@/pages/Tokens.svelte';
import Workflows from '@/pages/Workflows.svelte';
import WorkflowEditor from '@/pages/WorkflowEditor.svelte';
import Runs from '@/pages/Runs.svelte';
import Docs from '@/pages/Docs.svelte';
import Settings from '@/pages/Settings.svelte';
import NotFound from '@/pages/NotFound.svelte';

export default {
  '/': Home,
  '/providers': Providers,
  '/skills': Skills,
  '/chat': Chat,
  '/tokens': Tokens,
  '/workflows': Workflows,
  '/workflows/:id': WorkflowEditor,
  '/runs': Runs,
  '/docs': Docs,
  '/settings': Settings,
  '*': NotFound
};
