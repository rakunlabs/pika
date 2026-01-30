import Home from '@/pages/Home.svelte';
import Test from '@/pages/Test.svelte';
import NotFound from '@/pages/NotFound.svelte';

export default {
  '/': Home,
  '/test': Test,
  '*': NotFound
};
