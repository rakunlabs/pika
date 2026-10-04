package service_test

import (
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

func newVaultFilesService(t *testing.T) (*service.Service, string) {
	t.Helper()
	svc := newTestService(t)
	dir := t.TempDir()
	err := svc.PatchSettings(t.Context(), &service.PatchSettings{
		Action: service.ActionKeySet,
		VaultFiles: &service.VaultFilesSettings{
			Backend: service.VaultFilesBackendLocal,
			Local:   service.VaultFilesLocalSettings{Path: dir},
		},
	})
	if err != nil {
		t.Fatalf("PatchSettings: %v", err)
	}
	return svc, dir
}

func upload(t *testing.T, svc *service.Service, user, parent, rel, name, data string) *service.VaultFile {
	t.Helper()
	f, err := svc.UploadVaultFile(t.Context(), user, service.VaultUploadRequest{
		ParentID: parent, RelDir: rel, Name: name,
		Size: int64(len(data)), Body: strings.NewReader(data),
	})
	if err != nil {
		t.Fatalf("upload %s/%s: %v", rel, name, err)
	}
	return f
}

func TestVaultFilesUploadDownloadAndTree(t *testing.T) {
	svc, _ := newVaultFilesService(t)
	ctx := t.Context()

	f := upload(t, svc, "u1", "", "photos/2026", "a.txt", "hello")
	_, rc, err := svc.OpenVaultFile(ctx, "u1", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "hello" {
		t.Fatalf("content %q", b)
	}

	// Same folder path is reused, not duplicated; duplicate names auto-rename.
	g := upload(t, svc, "u1", "", "photos/2026", "a.txt", "again")
	if g.ParentID != f.ParentID || g.Name != "a (1).txt" {
		t.Fatalf("got parent=%s name=%s", g.ParentID, g.Name)
	}

	// Other users can't see or open it.
	if _, _, err := svc.OpenVaultFile(ctx, "u2", f.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("cross-user open: %v", err)
	}

	list, err := svc.ListVaultFiles(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if !list.Enabled || len(list.Files) != 4 { // photos, 2026, 2 files
		t.Fatalf("list: enabled=%v n=%d", list.Enabled, len(list.Files))
	}
}

func TestVaultFilesMoveRejectsCycle(t *testing.T) {
	svc, _ := newVaultFilesService(t)
	ctx := t.Context()

	a, err := svc.CreateVaultFolder(ctx, "u1", "", "a/b")
	if err != nil {
		t.Fatal(err)
	}
	b := a // deepest folder "b"
	parentA := b.ParentID
	if _, err := svc.UpdateVaultFile(ctx, "u1", parentA, service.VaultFilePatch{ParentID: &b.ID}); !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("expected cycle rejection, got %v", err)
	}

	f := upload(t, svc, "u1", "", "", "x.bin", "1")
	if _, err := svc.UpdateVaultFile(ctx, "u1", f.ID, service.VaultFilePatch{ParentID: &b.ID}); err != nil {
		t.Fatal(err)
	}
	other := upload(t, svc, "u1", "", "", "x.bin", "2")
	if _, err := svc.UpdateVaultFile(ctx, "u1", other.ID, service.VaultFilePatch{ParentID: &b.ID}); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("expected name conflict, got %v", err)
	}
}

func TestVaultFilesRecursiveDelete(t *testing.T) {
	svc, _ := newVaultFilesService(t)
	ctx := t.Context()

	f := upload(t, svc, "u1", "", "docs/deep", "a.txt", "x")
	upload(t, svc, "u1", "", "docs", "b.txt", "y")
	list, _ := svc.ListVaultFiles(ctx, "u1")
	var docs string
	for _, n := range list.Files {
		if n.Name == "docs" {
			docs = n.ID
		}
	}
	n, err := svc.DeleteVaultFile(ctx, "u1", docs)
	if err != nil || n != 4 {
		t.Fatalf("deleted=%d err=%v", n, err)
	}
	if _, err := svc.GetVaultFile(ctx, "u1", f.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("child survived: %v", err)
	}
}

func TestVaultFilesDisabled(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.UploadVaultFile(t.Context(), "u1", service.VaultUploadRequest{
		Name: "a", Size: 1, Body: strings.NewReader("a"),
	})
	if !errors.Is(err, service.ErrVaultFilesDisabled) {
		t.Fatalf("got %v", err)
	}
}

func TestVaultFilesSecretPreserved(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()
	set := func(secret string) error {
		return svc.PatchSettings(ctx, &service.PatchSettings{
			Action: service.ActionKeySet,
			VaultFiles: &service.VaultFilesSettings{
				Backend: service.VaultFilesBackendS3,
				S3: service.VaultFilesS3Settings{
					Endpoint: "https://s3.example.com", Bucket: "b",
					AccessKeyID: "AK", SecretAccessKey: secret,
				},
			},
		})
	}
	if err := set("s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := set(""); err != nil {
		t.Fatal(err)
	}
	s, _ := svc.Settings(ctx)
	if s.VaultFiles.S3.SecretAccessKey != "s3cret" {
		t.Fatalf("secret not preserved: %q", s.VaultFiles.S3.SecretAccessKey)
	}
}

func TestVaultFilesWriteContent(t *testing.T) {
	svc, dir := newVaultFilesService(t)
	ctx := t.Context()

	f := upload(t, svc, "u1", "", "", "notes.md", "# v1")
	countBlobs := func() int {
		n := 0
		_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, _ error) error {
			if d != nil && !d.IsDir() {
				n++
			}
			return nil
		})
		return n
	}

	body := "# v2\nedited"
	updated, err := svc.WriteVaultFileContent(ctx, "u1", f.ID, service.VaultContentRequest{
		ExpectedUpdatedAt: f.UpdatedAt,
		Size:              int64(len(body)),
		Body:              strings.NewReader(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Size != int64(len(body)) || updated.ContentType != f.ContentType {
		t.Fatalf("metadata not updated: %+v", updated)
	}
	_, rc, err := svc.OpenVaultFile(ctx, "u1", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != body {
		t.Fatalf("content %q", got)
	}
	if n := countBlobs(); n != 1 {
		t.Fatalf("old blob not removed, %d blobs on disk", n)
	}

	// Saving with the stale token must be rejected.
	_, err = svc.WriteVaultFileContent(ctx, "u1", f.ID, service.VaultContentRequest{
		ExpectedUpdatedAt: f.UpdatedAt,
		Size:              1,
		Body:              strings.NewReader("x"),
	})
	if !errors.Is(err, service.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if n := countBlobs(); n != 1 {
		t.Fatalf("rejected save leaked a blob, %d blobs on disk", n)
	}
}
