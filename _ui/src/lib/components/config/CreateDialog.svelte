<script lang="ts">
  import { X, Folder, File } from 'lucide-svelte';

  interface Props {
    isOpen: boolean;
    type: 'file' | 'folder';
    parentPath: string;
    onClose: () => void;
    onCreatePath: (fullPath: string, type: 'file' | 'folder') => void;
  }

  let { isOpen, type, parentPath, onClose, onCreatePath }: Props = $props();

  let fullPath = $state('');
  let error = $state('');

  $effect(() => {
    if (isOpen) {
      const prefix = parentPath ? `${parentPath}/` : '';
      fullPath = prefix + (type === 'folder' ? 'new-folder' : 'new-file');
      error = '';
    }
  });

  function validatePath(value: string): string | null {
    if (!value.trim()) {
      return 'Path is required';
    }
    if (value.startsWith('/')) {
      return 'Path should not start with /';
    }
    if (value.endsWith('/')) {
      return 'Path should not end with /';
    }
    if (/[<>:"|?*\\]/.test(value)) {
      return 'Path contains invalid characters';
    }
    if (value.split('/').some(s => !s.trim())) {
      return 'Path contains empty segments';
    }
    return null;
  }

  function handleSubmit(e: Event) {
    e.preventDefault();
    
    const validationError = validatePath(fullPath);
    if (validationError) {
      error = validationError;
      return;
    }

    onCreatePath(fullPath.trim(), type);
    onClose();
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      onClose();
    }
  }

  function handleBackdropClick(e: MouseEvent) {
    if (e.target === e.currentTarget) {
      onClose();
    }
  }
</script>

<svelte:window onkeydown={handleKeyDown} />

{#if isOpen}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div 
    class="fixed inset-0 bg-black/50 flex items-center justify-center z-[100] p-5"
    onclick={handleBackdropClick}
    onkeydown={(e) => e.key === 'Escape' && onClose()}
    role="dialog"
    aria-modal="true"
    aria-labelledby="create-dialog-title"
    tabindex="-1"
  >
    <div class="bg-white rounded-lg shadow-xl w-full max-w-[480px] overflow-hidden">
      <div class="flex items-center justify-between px-5 py-4 border-b border-slate-200 bg-slate-50">
        <div class="flex items-center gap-2.5 text-gray-700">
          {#if type === 'folder'}
            <Folder size={18} />
          {:else}
            <File size={18} />
          {/if}
          <h2 id="create-dialog-title" class="text-base font-semibold m-0">
            Create New {type === 'folder' ? 'Folder' : 'File'}
          </h2>
        </div>
        <button 
          class="flex items-center justify-center p-1.5 bg-transparent border-none rounded text-slate-500 cursor-pointer transition-all hover:bg-slate-200 hover:text-slate-800"
          onclick={onClose} 
          aria-label="Close"
        >
          <X size={18} />
        </button>
      </div>

      <form onsubmit={handleSubmit}>
        <div class="p-5">
          <div class="mb-3">
            <label for="path-input" class="block text-[13px] font-medium text-gray-700 mb-1.5">
              Full Path
            </label>
            <input
              id="path-input"
              type="text"
              bind:value={fullPath}
              placeholder={type === 'folder' ? 'path/to/folder' : 'path/to/file'}
              class="w-full px-3 py-2.5 text-sm font-mono border border-slate-200 rounded transition-all
                focus:outline-none focus:border-blue-500 focus:ring-[3px] focus:ring-blue-500/10
                {error ? 'border-red-600' : ''}"
              autofocus
            />
            {#if error}
              <span class="block mt-1.5 text-xs text-red-600">{error}</span>
            {/if}
          </div>

          <p class="text-xs text-slate-500 m-0">
            {#if type === 'file'}
              File format can be changed in the Settings panel after creation
            {:else}
              Parent folders will be created automatically if they don't exist
            {/if}
          </p>
        </div>

        <div class="flex justify-end gap-2.5 px-5 py-4 border-t border-slate-200 bg-slate-50">
          <button 
            type="button" 
            class="px-4 py-2 bg-white text-slate-600 border border-slate-200 rounded text-[13px] font-medium cursor-pointer transition-all hover:bg-slate-100"
            onclick={onClose}
          >
            Cancel
          </button>
          <button 
            type="submit" 
            class="px-4 py-2 bg-blue-500 text-white border-none rounded text-[13px] font-medium cursor-pointer transition-colors hover:bg-blue-600"
          >
            Create {type === 'folder' ? 'Folder' : 'File'}
          </button>
        </div>
      </form>
    </div>
  </div>
{/if}
