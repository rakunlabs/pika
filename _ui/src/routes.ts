import NotFound from '@/pages/NotFound.svelte';
import Settings from '@/pages/Settings.svelte';
import Configurations from '@/pages/Configurations.svelte';
import Users from '@/pages/Users.svelte';

export default {
  '/': Configurations,
  '/settings': Settings,
  '/configurations': Configurations,
  '/users': Users,
  '*': NotFound
};
