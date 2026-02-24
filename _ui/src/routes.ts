import Home from '@/pages/Home.svelte';
import Providers from '@/pages/Providers.svelte';
import Test from '@/pages/Test.svelte';
import Tokens from '@/pages/Tokens.svelte';
import Docs from '@/pages/Docs.svelte';
import Settings from '@/pages/Settings.svelte';
import NotFound from '@/pages/NotFound.svelte';

export default {
  '/': Home,
  '/providers': Providers,
  '/test': Test,
  '/tokens': Tokens,
  '/docs': Docs,
  '/settings': Settings,
  '*': NotFound
};
