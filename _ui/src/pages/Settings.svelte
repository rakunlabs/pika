<script lang="ts">
  import { storeNavbar } from "@/lib/store/store.svelte";

  storeNavbar.title = "Settings";
</script>

<div>
  <span>Connections</span>
  <form>
    <label>
      Username
      <input type="text" placeholder="Enter your username" />
    </label>

    <label>
      Email
      <input type="email" placeholder="Enter your email" />
    </label>

    <button type="submit">Save Settings</button>
  </form>
</div>
