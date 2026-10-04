<script lang="ts">
  import { untrack } from "svelte";
  import type { HookTarget } from "@/lib/types/config";
  import { addToast } from "@/lib/store/toast.svelte";
  import { inputClass } from "@/lib/ui";
  import { labelClass } from "./shared";

  type Props = { initial?: HookTarget["nats"] };
  let { initial }: Props = $props();

  const init = untrack(() => initial);
  let url = $state(init?.url ?? "");
  let subject = $state(init?.subject ?? "");
  let token = $state(init?.token || "");
  let username = $state(init?.username || "");
  let password = $state(init?.password || "");

  export function build(bodyTemplate: string | undefined): HookTarget | null {
    if (!url.trim()) {
      addToast("NATS URL is required", "alert");
      return null;
    }
    if (!subject.trim()) {
      addToast("NATS subject is required", "alert");
      return null;
    }
    return {
      type: "nats",
      nats: {
        url: url.trim(),
        subject: subject.trim(),
        token: token || undefined,
        username: username || undefined,
        password: password || undefined,
      },
      body_template: bodyTemplate,
    };
  }
</script>

<div class="grid grid-cols-2 gap-3 mb-3">
  <div>
    <label for="target-nats-url" class={labelClass}>URL</label>
    <input
      id="target-nats-url"
      type="text"
      bind:value={url}
      placeholder="nats://localhost:4222"
      class="{inputClass} font-mono"
    />
  </div>
  <div>
    <label for="target-nats-subject" class={labelClass}>Subject</label>
    <input
      id="target-nats-subject"
      type="text"
      bind:value={subject}
      placeholder="pika.events"
      class="{inputClass} font-mono"
    />
  </div>
</div>
<div class="mb-3">
  <label for="target-nats-token" class={labelClass}>Token (optional)</label>
  <input
    id="target-nats-token"
    type="password"
    bind:value={token}
    placeholder="Auth token"
    class="{inputClass} font-mono"
  />
</div>
<div class="grid grid-cols-2 gap-3 mb-3">
  <div>
    <label for="target-nats-username" class={labelClass}>Username (optional)</label>
    <input
      id="target-nats-username"
      type="text"
      bind:value={username}
      placeholder="Username"
      class={inputClass}
    />
  </div>
  <div>
    <label for="target-nats-password" class={labelClass}>Password (optional)</label>
    <input
      id="target-nats-password"
      type="password"
      bind:value={password}
      placeholder="Password"
      class={inputClass}
    />
  </div>
</div>
