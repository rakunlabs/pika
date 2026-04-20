<script lang="ts">
  import Router from "svelte-spa-router";
  import Navbar from "@/lib/components/Navbar.svelte";
  import Toast from "@/lib/components/Toast.svelte";
  import Login from "@/pages/Login.svelte";
  import routes from "@/routes";
  import { appStore } from "@/lib/store/store.svelte";

  $effect.root(() => {
    // boot-time: load both info and identity in parallel
    appStore.loadInfo();
    appStore.loadIdentity();
  });

  const authenticated = $derived(appStore.authenticated);
  const loading = $derived(authenticated === null);
</script>

<Toast />

{#if loading}
  <!-- Loading state while checking auth -->
  <div class="flex items-center justify-center h-full w-full bg-slate-100">
    <div class="text-sm text-slate-400">Loading...</div>
  </div>
{:else if authenticated === false}
  <Login />
{:else if authenticated === true}
  <div class="flex flex-col h-full w-full overflow-hidden bg-slate-100">
    <Navbar />
    <div class="flex-1 overflow-hidden">
      <Router {routes} />
    </div>
  </div>
{/if}
