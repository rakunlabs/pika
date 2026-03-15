import NotFound from '@/pages/NotFound.svelte';
import Settings from '@/pages/Settings.svelte';
import Configurations from '@/pages/Configurations.svelte';

export default {
  '/': Configurations,
  '/settings': Settings,
  '/configurations': Configurations,
  '*': NotFound
};
