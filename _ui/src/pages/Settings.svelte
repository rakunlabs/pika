<script lang="ts">
    import {
        Key,
        Globe,
        Webhook,
        RotateCw,
        ToggleLeft,
        HardDrive,
        ShieldCheck,
        Info,
        Users,
        Palette,
        KeyRound,
        Vault,
        Network,
        Plug,
    } from "lucide-svelte";
    import { appStore } from "@/lib/store/store.svelte";

    import AppearanceSection from "@/pages/settings/AppearanceSection.svelte";
    import AccountSecuritySection from "@/pages/settings/AccountSecuritySection.svelte";
    import VaultSection from "@/pages/settings/VaultSection.svelte";
    import TokensSection from "@/pages/settings/TokensSection.svelte";
    import ExternalResourcesSection from "@/pages/settings/ExternalResourcesSection.svelte";
    import HooksSection from "@/pages/settings/HooksSection.svelte";
    import AuthSection from "@/pages/settings/AuthSection.svelte";
    import UserSyncSection from "@/pages/settings/UserSyncSection.svelte";
    import KeyRotationSection from "@/pages/settings/KeyRotationSection.svelte";
    import FeaturesSection from "@/pages/settings/FeaturesSection.svelte";
    import ClusterSection from "@/pages/settings/ClusterSection.svelte";
    import PublicEndpointsSection from "@/pages/settings/PublicEndpointsSection.svelte";
    import BackupSection from "@/pages/settings/BackupSection.svelte";
    import AboutSection from "@/pages/settings/AboutSection.svelte";

    type Section =
        | "appearance"
        | "account_security"
        | "vault"
        | "tokens"
        | "external"
        | "hooks"
        | "auth"
        | "user_sync"
        | "rotation"
        | "features"
        | "cluster"
        | "public_endpoints"
        | "backup"
        | "about";

    // Section → required capability. `null` means always visible (e.g.
    // About, Appearance, Account Security — these are per-user and
    // intentionally require no capability). 'vault' is per-user too;
    // its visibility is also further filtered below by vault_enabled.
    const sectionCaps: Record<Section, string | null> = {
        appearance: null,
        account_security: null,
        vault: null,
        tokens: "tokens.manage",
        external: "settings.manage",
        hooks: "settings.manage",
        auth: "settings.manage",
        user_sync: "settings.manage",
        rotation: "settings.manage",
        features: "settings.manage",
        cluster: "settings.manage",
        public_endpoints: "settings.manage",
        backup: "settings.manage",
        about: null,
    };

    const allSections: { key: Section; label: string; icon: typeof Key }[] = [
        { key: "appearance", label: "Appearance", icon: Palette },
        { key: "account_security", label: "Account Security", icon: KeyRound },
        { key: "vault", label: "Personal Vault", icon: Vault },
        { key: "tokens", label: "Access Tokens", icon: Key },
        { key: "external", label: "External Resources", icon: Globe },
        { key: "hooks", label: "Hooks", icon: Webhook },
        { key: "auth", label: "Authentication", icon: ShieldCheck },
        { key: "user_sync", label: "User Sync", icon: Users },
        { key: "rotation", label: "Key Rotation", icon: RotateCw },
        { key: "features", label: "Features", icon: ToggleLeft },
        { key: "cluster", label: "Cluster", icon: Network },
        { key: "public_endpoints", label: "Endpoints", icon: Plug },
        { key: "backup", label: "Backup", icon: HardDrive },
        { key: "about", label: "About", icon: Info },
    ];

    // Filter the section list by the current user's capabilities. About is
    // always shown so users without any setting permission still land on
    // something useful instead of an empty page. The Personal Vault entry
    // is additionally gated on the server-level vault_enabled flag so we
    // don't show a panel that 503s its own data fetches.
    const sections = $derived(
        allSections.filter((s) => {
            if (s.key === "vault" && !(appStore.info?.vault_enabled ?? false)) {
                return false;
            }
            const cap = sectionCaps[s.key];
            return cap === null || appStore.hasPermission(cap);
        }),
    );

    // Default to 'appearance' (always present for any logged-in user). The
    // effect below snaps to the first visible section once permissions are
    // resolved, and re-snaps if the current active section ever becomes
    // inaccessible.
    let activeSection = $state<Section>("appearance");
    $effect(() => {
        if (!sections.some((s) => s.key === activeSection)) {
            activeSection = sections[0]?.key ?? "appearance";
        }
    });
</script>

<div class="flex h-full overflow-hidden bg-slate-100 dark:bg-warm-900">
    <!-- Left Sidebar -->
    <div
        class="w-52 shrink-0 bg-slate-50 dark:bg-warm-800 border-r border-slate-200 dark:border-warm-700 overflow-y-auto"
    >
        <nav class="flex flex-col gap-0.5 px-2 pt-3 pb-4">
            {#each sections as section}
                <button
                    class="flex items-center gap-2.5 w-full px-3 py-2 text-[13px] font-medium rounded-md cursor-pointer transition-colors text-left
  {activeSection === section.key
                        ? 'bg-accent-50 text-accent-700 border border-accent-200 dark:bg-accent-900/40 dark:text-accent-300 dark:border-accent-700'
                        : 'bg-transparent text-slate-600 dark:text-warm-200 border border-transparent hover:bg-slate-100 dark:hover:bg-warm-700 hover:text-slate-800 dark:hover:text-white'}"
                    onclick={() => (activeSection = section.key)}
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
            {#if activeSection === "appearance"}
                <AppearanceSection />
            {:else if activeSection === "account_security"}
                <AccountSecuritySection />
            {:else if activeSection === "vault"}
                <VaultSection />
            {:else if activeSection === "tokens"}
                <TokensSection />
            {:else if activeSection === "external"}
                <ExternalResourcesSection />
            {:else if activeSection === "hooks"}
                <HooksSection />
            {:else if activeSection === "auth"}
                <AuthSection />
            {:else if activeSection === "user_sync"}
                <UserSyncSection />
            {:else if activeSection === "rotation"}
                <KeyRotationSection />
            {:else if activeSection === "features"}
                <FeaturesSection />
            {:else if activeSection === "cluster"}
                <ClusterSection />
            {:else if activeSection === "public_endpoints"}
                <PublicEndpointsSection />
            {:else if activeSection === "backup"}
                <BackupSection />
            {:else if activeSection === "about"}
                <AboutSection />
            {/if}
        </div>
    </div>
</div>
