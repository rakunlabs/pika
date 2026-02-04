import Home from '@/pages/Home.svelte';
import Test from '@/pages/Test.svelte';
import NotFound from '@/pages/NotFound.svelte';
import Settings from '@/pages/Settings.svelte';
import Configurations from '@/pages/Configurations.svelte';

export default {
  '/': Home,
  '/test': Test,
  '/settings': Settings,
  '/configurations': Configurations,
  '*': NotFound
};
