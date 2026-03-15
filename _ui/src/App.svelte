<script lang="ts">
  import Router from "svelte-spa-router";
  import Navbar from "@/lib/components/Navbar.svelte";
  import Toast from "@/lib/components/Toast.svelte";
  import routes from "@/routes";
</script>

<Toast />

<div class="flex flex-col h-full w-full overflow-hidden bg-slate-100">
  <Navbar />
  <div class="flex-1 overflow-hidden">
    <Router {routes} />
  </div>
</div>
