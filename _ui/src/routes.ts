import NotFound from '@/pages/NotFound.svelte';
import Settings from '@/pages/Settings.svelte';
import Configurations from '@/pages/Configurations.svelte';
import Users from '@/pages/Users.svelte';
import Vault from '@/pages/Vault.svelte';
import External from '@/pages/External.svelte';

export default {
  '/': Configurations,
  '/settings': Settings,
  '/configurations': Configurations,
  '/vault': Vault,
  '/external': External,
  '/users': Users,
  '*': NotFound
};
