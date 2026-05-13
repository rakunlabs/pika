import NotFound from '@/pages/NotFound.svelte';
import Settings from '@/pages/Settings.svelte';
import Configurations from '@/pages/Configurations.svelte';
import Files from '@/pages/Files.svelte';
import Users from '@/pages/Users.svelte';
import Vault from '@/pages/Vault.svelte';

export default {
  '/': Configurations,
  '/settings': Settings,
  '/configurations': Configurations,
  '/files': Files,
  '/vault': Vault,
  '/users': Users,
  '*': NotFound
};
