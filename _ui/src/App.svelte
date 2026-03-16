<script lang="ts">
  import Router from "svelte-spa-router";
  import Navbar from "@/lib/components/Navbar.svelte";
  import Toast from "@/lib/components/Toast.svelte";
  import Login from "@/pages/Login.svelte";
  import Setup from "@/pages/Setup.svelte";
  import routes from "@/routes";
  import { appStore } from "@/lib/store/store.svelte";
  import { onMount } from "svelte";

  onMount(() => {
    appStore.loadInfo();
  });

  const authenticated = $derived(appStore.authenticated);
  const authEnabled = $derived(appStore.info?.auth_enabled ?? false);
  const setupRequired = $derived(appStore.info?.setup_required ?? false);
  const showSetup = $derived(authEnabled && setupRequired && authenticated === false);
  const showLogin = $derived(authEnabled && !setupRequired && authenticated === false);
  const showApp = $derived(!authEnabled || authenticated === true);
  const loading = $derived(authenticated === null);
</script>

<Toast />

{#if loading}
  <!-- Loading state while checking auth -->
  <div class="flex items-center justify-center h-full w-full bg-slate-100">
    <div class="text-sm text-slate-400">Loading...</div>
  </div>
{:else if showSetup}
  <Setup />
{:else if showLogin}
  <Login />
{:else if showApp}
  <div class="flex flex-col h-full w-full overflow-hidden bg-slate-100">
    <Navbar />
    <div class="flex-1 overflow-hidden">
      <Router {routes} />
    </div>
  </div>
{/if}
