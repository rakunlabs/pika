package hook

import (
	"io"

	"github.com/rakunlabs/pika/internal/rawfs"
)

// hookedBase contains common fields for all hooked filesystem wrappers.
type hookedBase struct {
	inner      rawfs.RawFS
	dispatcher *Dispatcher
	mount      string
	protocol   string
}

func (h *hookedBase) Stat(path string) (*rawfs.FileInfo, error) {
	return h.inner.Stat(path)
}

func (h *hookedBase) ReadDir(path string) ([]rawfs.DirEntry, error) {
	return h.inner.ReadDir(path)
}

func (h *hookedBase) Open(path string) (rawfs.ReadSeekCloser, *rawfs.FileInfo, error) {
	return h.inner.Open(path)
}

// emitWrite emits a file.created or file.updated event.
func (h *hookedBase) emitWrite(path string, size int64) {
	eventType := EventFileCreated
	if _, err := h.inner.Stat(path); err == nil {
		eventType = EventFileUpdated
	}
	h.dispatcher.Emit(Event{
		Type:     eventType,
		Mount:    h.mount,
		Path:     path,
		Size:     size,
		Protocol: h.protocol,
	})
}

// --- Wrapper types with increasing capability ---
// These exist so that type assertions (IsWritable, IsRenamable, IsCopyable)
// work correctly based on what the underlying FS actually supports.

// hookedReadOnly wraps a read-only FS. No write events.
type hookedReadOnly struct {
	hookedBase
}

// hookedWritable wraps a FS that supports Write/Delete/MkDir.
type hookedWritable struct {
	hookedBase
}

func (h *hookedWritable) Write(path string, r io.Reader, size int64) error {
	wfs := h.inner.(rawfs.WritableRawFS)

	// Check existence before write to determine create vs update.
	eventType := EventFileCreated
	if _, err := h.inner.Stat(path); err == nil {
		eventType = EventFileUpdated
	}

	if err := wfs.Write(path, r, size); err != nil {
		return err
	}

	h.dispatcher.Emit(Event{
		Type:     eventType,
		Mount:    h.mount,
		Path:     path,
		Size:     size,
		Protocol: h.protocol,
	})
	return nil
}

func (h *hookedWritable) Delete(path string) error {
	wfs := h.inner.(rawfs.WritableRawFS)
	if err := wfs.Delete(path); err != nil {
		return err
	}
	h.dispatcher.Emit(Event{
		Type:     EventFileDeleted,
		Mount:    h.mount,
		Path:     path,
		Protocol: h.protocol,
	})
	return nil
}

func (h *hookedWritable) MkDir(path string) error {
	wfs := h.inner.(rawfs.WritableRawFS)
	if err := wfs.MkDir(path); err != nil {
		return err
	}
	h.dispatcher.Emit(Event{
		Type:     EventDirCreated,
		Mount:    h.mount,
		Path:     path,
		Protocol: h.protocol,
	})
	return nil
}

// hookedWritableRenamable wraps a FS that supports Write + Rename.
type hookedWritableRenamable struct {
	hookedWritable
}

func (h *hookedWritableRenamable) Rename(oldPath, newPath string) error {
	rfs := h.inner.(rawfs.RenamableRawFS)
	if err := rfs.Rename(oldPath, newPath); err != nil {
		return err
	}
	h.dispatcher.Emit(Event{
		Type:     EventFileRenamed,
		Mount:    h.mount,
		Path:     newPath,
		OldPath:  oldPath,
		Protocol: h.protocol,
	})
	return nil
}

// hookedWritableCopyable wraps a FS that supports Write + Copy.
type hookedWritableCopyable struct {
	hookedWritable
}

func (h *hookedWritableCopyable) Copy(srcPath, dstPath string) error {
	cfs := h.inner.(rawfs.CopyableRawFS)
	if err := cfs.Copy(srcPath, dstPath); err != nil {
		return err
	}
	h.dispatcher.Emit(Event{
		Type:     EventFileCopied,
		Mount:    h.mount,
		Path:     dstPath,
		OldPath:  srcPath,
		Protocol: h.protocol,
	})
	return nil
}

// hookedWritableRenamableCopyable wraps a FS with Write + Rename + Copy.
type hookedWritableRenamableCopyable struct {
	hookedWritable
}

func (h *hookedWritableRenamableCopyable) Rename(oldPath, newPath string) error {
	rfs := h.inner.(rawfs.RenamableRawFS)
	if err := rfs.Rename(oldPath, newPath); err != nil {
		return err
	}
	h.dispatcher.Emit(Event{
		Type:     EventFileRenamed,
		Mount:    h.mount,
		Path:     newPath,
		OldPath:  oldPath,
		Protocol: h.protocol,
	})
	return nil
}

func (h *hookedWritableRenamableCopyable) Copy(srcPath, dstPath string) error {
	cfs := h.inner.(rawfs.CopyableRawFS)
	if err := cfs.Copy(srcPath, dstPath); err != nil {
		return err
	}
	h.dispatcher.Emit(Event{
		Type:     EventFileCopied,
		Mount:    h.mount,
		Path:     dstPath,
		OldPath:  srcPath,
		Protocol: h.protocol,
	})
	return nil
}

// hookedRenamable wraps a read-only FS that supports Rename (e.g. FTP/SFTP backends).
type hookedRenamable struct {
	hookedBase
}

func (h *hookedRenamable) Rename(oldPath, newPath string) error {
	rfs := h.inner.(rawfs.RenamableRawFS)
	if err := rfs.Rename(oldPath, newPath); err != nil {
		return err
	}
	h.dispatcher.Emit(Event{
		Type:     EventFileRenamed,
		Mount:    h.mount,
		Path:     newPath,
		OldPath:  oldPath,
		Protocol: h.protocol,
	})
	return nil
}

// NewHookedFS wraps the given filesystem with hook event emission.
// The returned RawFS implements only the interfaces the inner FS supports,
// so type assertions like rawfs.IsWritable() work correctly.
// If dispatcher is nil, the inner FS is returned unchanged.
func NewHookedFS(inner rawfs.RawFS, dispatcher *Dispatcher, mount, protocol string) rawfs.RawFS {
	if dispatcher == nil {
		return inner
	}

	base := hookedBase{
		inner:      inner,
		dispatcher: dispatcher,
		mount:      mount,
		protocol:   protocol,
	}

	_, writable := inner.(rawfs.WritableRawFS)
	_, renamable := inner.(rawfs.RenamableRawFS)
	_, copyable := inner.(rawfs.CopyableRawFS)

	switch {
	case writable && renamable && copyable:
		return &hookedWritableRenamableCopyable{hookedWritable{base}}
	case writable && renamable:
		return &hookedWritableRenamable{hookedWritable{base}}
	case writable && copyable:
		return &hookedWritableCopyable{hookedWritable{base}}
	case writable:
		return &hookedWritable{base}
	case renamable:
		return &hookedRenamable{base}
	default:
		return &hookedReadOnly{base}
	}
}

// Inner returns the underlying raw filesystem from a hooked wrapper.
// If the FS is not a hooked wrapper, it returns the FS itself.
func Inner(fs rawfs.RawFS) rawfs.RawFS {
	switch h := fs.(type) {
	case *hookedReadOnly:
		return h.inner
	case *hookedWritable:
		return h.inner
	case *hookedWritableRenamable:
		return h.inner
	case *hookedWritableCopyable:
		return h.inner
	case *hookedWritableRenamableCopyable:
		return h.inner
	case *hookedRenamable:
		return h.inner
	default:
		return fs
	}
}

// Compile-time interface checks.
var (
	_ rawfs.RawFS         = (*hookedReadOnly)(nil)
	_ rawfs.RawFS         = (*hookedWritable)(nil)
	_ rawfs.WritableRawFS = (*hookedWritable)(nil)

	_ rawfs.WritableRawFS  = (*hookedWritableRenamable)(nil)
	_ rawfs.RenamableRawFS = (*hookedWritableRenamable)(nil)

	_ rawfs.WritableRawFS = (*hookedWritableCopyable)(nil)
	_ rawfs.CopyableRawFS = (*hookedWritableCopyable)(nil)

	_ rawfs.WritableRawFS  = (*hookedWritableRenamableCopyable)(nil)
	_ rawfs.RenamableRawFS = (*hookedWritableRenamableCopyable)(nil)
	_ rawfs.CopyableRawFS  = (*hookedWritableRenamableCopyable)(nil)

	_ rawfs.RenamableRawFS = (*hookedRenamable)(nil)
)
