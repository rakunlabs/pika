<script lang="ts">
  import { Key, Globe, FolderOpen, Share2, Server, Webhook, RotateCw, Lock, HardDrive, ShieldCheck, Info } from "lucide-svelte";

  import TokensSection from "@/pages/settings/TokensSection.svelte";
  import ExternalResourcesSection from "@/pages/settings/ExternalResourcesSection.svelte";
  import RawMountsSection from "@/pages/settings/RawMountsSection.svelte";
  import FileSharingSection from "@/pages/settings/FileSharingSection.svelte";
  import FileServersSection from "@/pages/settings/FileServersSection.svelte";
  import PublicServerSection from "@/pages/settings/PublicServerSection.svelte";
  import HooksSection from "@/pages/settings/HooksSection.svelte";
  import AuthSection from "@/pages/settings/AuthSection.svelte";
  import KeyRotationSection from "@/pages/settings/KeyRotationSection.svelte";
  import SecuritySection from "@/pages/settings/SecuritySection.svelte";
  import BackupSection from "@/pages/settings/BackupSection.svelte";
  import AboutSection from "@/pages/settings/AboutSection.svelte";

  type Section = 'tokens' | 'external' | 'raw_mounts' | 'ftp_shares' | 'file_servers' | 'public_server' | 'hooks' | 'auth' | 'rotation' | 'security' | 'backup' | 'about';

  let activeSection = $state<Section>('tokens');

  const sections: { key: Section; label: string; icon: typeof Key }[] = [
    { key: 'tokens',        label: 'Access Tokens',     icon: Key },
    { key: 'external',      label: 'External Resources', icon: Globe },
    { key: 'raw_mounts',    label: 'Raw Mounts',        icon: FolderOpen },
    { key: 'ftp_shares',    label: 'File Sharing',      icon: Share2 },
    { key: 'file_servers',  label: 'File Servers',      icon: Server },
    { key: 'public_server', label: 'Public Server',     icon: Globe },
    { key: 'hooks',         label: 'Hooks',             icon: Webhook },
    { key: 'auth',          label: 'Authentication',    icon: ShieldCheck },
    { key: 'rotation',      label: 'Key Rotation',      icon: RotateCw },
    { key: 'security',      label: 'Security',          icon: Lock },
    { key: 'backup',        label: 'Backup',            icon: HardDrive },
    { key: 'about',         label: 'About',             icon: Info },
  ];
</script>

<div class="flex h-full overflow-hidden">
  <!-- Left Sidebar -->
  <div class="w-52 shrink-0 bg-slate-50 border-r border-slate-200 overflow-y-auto">
    <div class="px-3 py-3">
      <span class="text-[10px] font-semibold text-slate-400 uppercase tracking-wider px-2">Settings</span>
    </div>
    <nav class="flex flex-col gap-0.5 px-2 pb-4">
      {#each sections as section}
        <button
          class="flex items-center gap-2.5 w-full px-3 py-2 text-[13px] font-medium rounded-md cursor-pointer transition-colors text-left
            {activeSection === section.key ? 'bg-blue-50 text-blue-700 border border-blue-200' : 'bg-transparent text-slate-600 border border-transparent hover:bg-slate-100 hover:text-slate-800'}"
          onclick={() => activeSection = section.key}
        >
          <section.icon size={15} class="shrink-0" />
          {section.label}
        </button>
      {/each}
    </nav>
  </div>

  <!-- Right Content Area -->
  <div class="flex-1 overflow-y-auto">
    <div class="max-w-3xl p-6">
      {#if activeSection === 'tokens'}
        <TokensSection />
      {:else if activeSection === 'external'}
        <ExternalResourcesSection />
      {:else if activeSection === 'raw_mounts'}
        <RawMountsSection />
      {:else if activeSection === 'ftp_shares'}
        <FileSharingSection />
      {:else if activeSection === 'file_servers'}
        <FileServersSection />
      {:else if activeSection === 'public_server'}
        <PublicServerSection />
      {:else if activeSection === 'hooks'}
        <HooksSection />
      {:else if activeSection === 'auth'}
        <AuthSection />
      {:else if activeSection === 'rotation'}
        <KeyRotationSection />
      {:else if activeSection === 'security'}
        <SecuritySection />
      {:else if activeSection === 'backup'}
        <BackupSection />
      {:else if activeSection === 'about'}
        <AboutSection />
      {/if}
    </div>
  </div>
</div>
