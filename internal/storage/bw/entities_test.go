package bw

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/rakunlabs/pika/internal/service"
)

func fileEntryKeys(entries []service.FileEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path+"@"+strconv.FormatInt(e.Version, 10))
	}
	sort.Strings(out)
	return out
}

func TestFileStorage_VersionsAndDelete(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	files := store.Files()

	set := func(path string, v int64, data string) {
		t.Helper()
		if err := files.Set(ctx, path, v, &service.File{Data: []byte(data)}); err != nil {
			t.Fatalf("Set(%s,%d): %v", path, v, err)
		}
	}
	set("app/a", 1, "a1")
	set("app/a", 2, "a2")
	set("app/b", 1, "b1")
	set("app2/c", 1, "c1") // shares the "app" prefix string but not the folder
	set("app", 1, "root")

	got, err := files.Get(ctx, "app/a", 2)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Data) != "a2" {
		t.Errorf("Get(app/a,2) = %q", got.Data)
	}
	got, err = files.Get(ctx, "app/a", 1)
	if err != nil || string(got.Data) != "a1" {
		t.Errorf("Get(app/a,1) = %v, %v", got, err)
	}
	if _, err := files.Get(ctx, "app/a", 3); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get(missing version) err = %v; want ErrNotFound", err)
	}

	// Overwrite an existing version.
	set("app/a", 2, "a2-new")
	got, _ = files.Get(ctx, "app/a", 2)
	if string(got.Data) != "a2-new" {
		t.Errorf("overwrite = %q", got.Data)
	}

	list, err := files.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if want := []string{"app/a@1", "app/a@2", "app/b@1", "app2/c@1", "app@1"}; !slices.Equal(fileEntryKeys(list), want) {
		t.Errorf("List = %v; want %v", fileEntryKeys(list), want)
	}
	for _, e := range list {
		if e.File == nil {
			t.Errorf("List entry %s has nil File", e.Path)
		}
	}

	if err := files.Delete(ctx, "app/a", 1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := files.Get(ctx, "app/a", 1); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get after Delete err = %v", err)
	}
	if _, err := files.Get(ctx, "app/a", 2); err != nil {
		t.Errorf("sibling version removed by Delete: %v", err)
	}

	set("app/a", 3, "a3")
	if err := files.DeleteAllVersions(ctx, "app/a"); err != nil {
		t.Fatalf("DeleteAllVersions: %v", err)
	}
	list, _ = files.List(ctx)
	if want := []string{"app/b@1", "app2/c@1", "app@1"}; !slices.Equal(fileEntryKeys(list), want) {
		t.Errorf("after DeleteAllVersions = %v; want %v", fileEntryKeys(list), want)
	}

	if err := files.DeletePrefix(ctx, "app"); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	list, _ = files.List(ctx)
	if want := []string{"app2/c@1"}; !slices.Equal(fileEntryKeys(list), want) {
		t.Errorf("after DeletePrefix = %v; want %v", fileEntryKeys(list), want)
	}

	if err := files.DeleteAll(ctx); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	list, _ = files.List(ctx)
	if len(list) != 0 {
		t.Errorf("after DeleteAll = %v", fileEntryKeys(list))
	}
}

func TestFileStorage_BinaryData(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	data := []byte{0x00, 0xFF, 0x10, 0x00, 'x'}
	if err := store.Files().Set(ctx, "bin", 7, &service.File{Data: data}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Files().Get(ctx, "bin", 7)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.Data, data) {
		t.Errorf("data = %v; want %v", got.Data, data)
	}
}

func TestFileVersionStorage(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	fv := store.FileVersions()

	versions := service.FileVersions{
		{Version: 1, Status: []service.FileStatus{{Status: "draft", Timestamp: 10, Author: "alice"}}},
		{Version: 2, Constraint: ">= 1.0.0"},
	}
	for _, p := range []string{"svc/a", "svc/b", "svc2/x", "svc"} {
		if err := fv.Set(ctx, p, versions); err != nil {
			t.Fatalf("Set(%s): %v", p, err)
		}
	}

	got, err := fv.Get(ctx, "svc/a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 2 || got[0].Version != 1 || got[1].Constraint != ">= 1.0.0" {
		t.Errorf("Get = %+v", got)
	}
	if len(got[0].Status) != 1 || got[0].Status[0].Author != "alice" || got[0].Status[0].Status != "draft" {
		t.Errorf("Status = %+v", got[0].Status)
	}
	if _, err := fv.Get(ctx, "missing"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get(missing) err = %v", err)
	}

	// Set replaces the whole slice.
	if err := fv.Set(ctx, "svc/a", service.FileVersions{{Version: 9}}); err != nil {
		t.Fatalf("Set replace: %v", err)
	}
	got, _ = fv.Get(ctx, "svc/a")
	if len(got) != 1 || got[0].Version != 9 {
		t.Errorf("after replace = %+v", got)
	}

	paths := func() []string {
		t.Helper()
		list, err := fv.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		out := make([]string, 0, len(list))
		for _, e := range list {
			out = append(out, e.Path)
		}
		sort.Strings(out)
		return out
	}
	if want := []string{"svc", "svc/a", "svc/b", "svc2/x"}; !slices.Equal(paths(), want) {
		t.Errorf("List = %v; want %v", paths(), want)
	}

	if err := fv.Delete(ctx, "svc/b"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if want := []string{"svc", "svc/a", "svc2/x"}; !slices.Equal(paths(), want) {
		t.Errorf("after Delete = %v; want %v", paths(), want)
	}

	if err := fv.DeletePrefix(ctx, "svc"); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	if want := []string{"svc2/x"}; !slices.Equal(paths(), want) {
		t.Errorf("after DeletePrefix = %v; want %v", paths(), want)
	}

	if err := fv.DeleteAll(ctx); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	if got := paths(); len(got) != 0 {
		t.Errorf("after DeleteAll = %v", got)
	}
}

func TestFileStorage_TxRollback(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	if err := store.Files().Set(ctx, "keep", 1, &service.File{Data: []byte("k")}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	errBoom := errors.New("boom")
	err := store.Tx(ctx, func(ctx context.Context, tx service.Storage) error {
		if err := tx.Files().Set(ctx, "new", 1, &service.File{Data: []byte("n")}); err != nil {
			return err
		}
		if err := tx.FileVersions().Set(ctx, "new", service.FileVersions{{Version: 1}}); err != nil {
			return err
		}
		if err := tx.Files().DeleteAll(ctx); err != nil {
			return err
		}
		list, err := tx.Files().List(ctx)
		if err != nil {
			return err
		}
		if len(list) != 0 {
			t.Errorf("in-tx DeleteAll not visible: %d entries", len(list))
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Tx err = %v; want errBoom", err)
	}

	if _, err := store.Files().Get(ctx, "keep", 1); err != nil {
		t.Errorf("rolled-back delete removed 'keep': %v", err)
	}
	if _, err := store.Files().Get(ctx, "new", 1); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("rolled-back insert visible: %v", err)
	}
	if _, err := store.FileVersions().Get(ctx, "new"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("rolled-back version insert visible: %v", err)
	}
}

func TestSessionStorage(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	sessions := store.Sessions()

	now := time.Now().UTC().Truncate(time.Microsecond)
	mk := func(id, user string, created time.Time, ttl time.Duration) {
		t.Helper()
		if err := sessions.Create(ctx, &service.Session{
			ID: id, UserID: user, Username: user + "-name", Payload: []byte("p-" + id),
			RefreshID: "r-" + id, CreatedAt: created, ExpiresAt: now.Add(ttl),
		}); err != nil {
			t.Fatalf("Create(%s): %v", id, err)
		}
	}
	mk("s1", "u1", now.Add(-3*time.Minute), time.Hour)
	mk("s2", "u1", now.Add(-1*time.Minute), time.Hour)
	mk("s3", "u1", now.Add(-2*time.Minute), -time.Minute) // expired
	mk("s4", "u2", now, time.Hour)

	got, err := sessions.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UserID != "u1" || got.Username != "u1-name" || string(got.Payload) != "p-s1" || got.RefreshID != "r-s1" {
		t.Errorf("Get = %+v", got)
	}
	if !got.CreatedAt.Equal(now.Add(-3*time.Minute)) || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("times = %v / %v", got.CreatedAt, got.ExpiresAt)
	}
	if _, err := sessions.Get(ctx, "nope"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get(missing) err = %v", err)
	}

	n, err := sessions.CountByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("CountByUserID: %v", err)
	}
	if n != 2 {
		t.Errorf("CountByUserID(u1) = %d; want 2 (expired excluded)", n)
	}

	list, err := sessions.ListByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListByUserID: %v", err)
	}
	if len(list) != 2 || list[0].ID != "s2" || list[1].ID != "s1" {
		ids := []string{}
		for _, s := range list {
			ids = append(ids, s.ID)
		}
		t.Errorf("ListByUserID(u1) = %v; want [s2 s1]", ids)
	}

	if err := sessions.DeleteExpired(ctx); err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if _, err := sessions.Get(ctx, "s3"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("expired session survived DeleteExpired: %v", err)
	}
	if _, err := sessions.Get(ctx, "s1"); err != nil {
		t.Errorf("live session removed by DeleteExpired: %v", err)
	}

	if err := sessions.Delete(ctx, "s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := sessions.Get(ctx, "s1"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get after Delete err = %v", err)
	}

	if err := sessions.DeleteByUserID(ctx, "u1"); err != nil {
		t.Fatalf("DeleteByUserID: %v", err)
	}
	if n, _ := sessions.CountByUserID(ctx, "u1"); n != 0 {
		t.Errorf("CountByUserID after DeleteByUserID = %d", n)
	}
	if n, _ := sessions.CountByUserID(ctx, "u2"); n != 1 {
		t.Errorf("other user's sessions affected: count = %d", n)
	}
}

func newVaultItem(id, user string) *service.VaultItem {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return &service.VaultItem{
		ID:               id,
		UserID:           user,
		Type:             "login",
		EncryptedTitle:   []byte("title-" + id),
		EncryptedPayload: []byte("payload-" + id),
		CreatedAt:        now,
		UpdatedAt:        now,
		Version:          1,
	}
}

func TestVaultItemStorage_OwnershipAndValidation(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	items := store.VaultItems()

	if err := items.Create(ctx, nil); !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("Create(nil) err = %v", err)
	}
	if err := items.Create(ctx, &service.VaultItem{ID: "x"}); !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("Create(no user) err = %v", err)
	}

	if err := items.Create(ctx, newVaultItem("i1", "alice")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := items.Create(ctx, newVaultItem("i1", "alice")); !errors.Is(err, service.ErrConflict) {
		t.Errorf("Create duplicate err = %v; want ErrConflict", err)
	}

	got, err := items.Get(ctx, "alice", "i1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.EncryptedPayload) != "payload-i1" || got.Type != "login" {
		t.Errorf("Get = %+v", got)
	}
	if _, err := items.Get(ctx, "mallory", "i1"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("cross-user Get err = %v; want ErrNotFound", err)
	}
	if _, err := items.Get(ctx, "", "i1"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get(empty user) err = %v; want ErrNotFound", err)
	}

	stolen := newVaultItem("i1", "mallory")
	if err := items.Update(ctx, stolen); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("cross-user Update err = %v; want ErrNotFound", err)
	}
	if got, _ := items.Get(ctx, "alice", "i1"); got == nil || got.UserID != "alice" {
		t.Errorf("cross-user Update re-keyed the item: %+v", got)
	}
	if err := items.Update(ctx, newVaultItem("missing", "alice")); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Update(missing) err = %v; want ErrNotFound", err)
	}

	upd := newVaultItem("i1", "alice")
	upd.EncryptedPayload = []byte("payload-v2")
	upd.Version = 2
	if err := items.Update(ctx, upd); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = items.Get(ctx, "alice", "i1")
	if string(got.EncryptedPayload) != "payload-v2" || got.Version != 2 {
		t.Errorf("after Update = %+v", got)
	}

	if err := items.Delete(ctx, "mallory", "i1"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("cross-user Delete err = %v; want ErrNotFound", err)
	}
	if err := items.Delete(ctx, "", "i1"); !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("Delete(empty user) err = %v; want ErrBadRequest", err)
	}
	if err := items.Delete(ctx, "alice", "i1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := items.Get(ctx, "alice", "i1"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Get after Delete err = %v", err)
	}
}

func TestVaultItemStorage_ListFilters(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	items := store.VaultItems()

	deleted := time.Now().UTC()
	fixtures := []*service.VaultItem{
		newVaultItem("active", "alice"),
		newVaultItem("fav", "alice"),
		newVaultItem("archived", "alice"),
		newVaultItem("trash", "alice"),
		newVaultItem("note", "alice"),
		newVaultItem("other", "bob"),
	}
	fixtures[1].Favorite = true
	fixtures[2].Archived = true
	fixtures[3].DeletedAt = &deleted
	fixtures[4].Type = "note"
	for _, it := range fixtures {
		if err := items.Create(ctx, it); err != nil {
			t.Fatalf("Create(%s): %v", it.ID, err)
		}
	}

	ids := func(f service.VaultItemFilter) []string {
		t.Helper()
		list, err := items.List(ctx, "alice", f)
		if err != nil {
			t.Fatalf("List(%+v): %v", f, err)
		}
		out := make([]string, 0, len(list))
		for _, it := range list {
			out = append(out, it.ID)
		}
		sort.Strings(out)
		return out
	}

	cases := []struct {
		name string
		f    service.VaultItemFilter
		want []string
	}{
		{"default", service.VaultItemFilter{}, []string{"active", "fav", "note"}},
		{"include archived", service.VaultItemFilter{IncludeArchived: true}, []string{"active", "archived", "fav", "note"}},
		{"archived only", service.VaultItemFilter{ArchivedOnly: true}, []string{"archived"}},
		{"trash only", service.VaultItemFilter{TrashOnly: true}, []string{"trash"}},
		{"favorite only", service.VaultItemFilter{FavoriteOnly: true}, []string{"fav"}},
		{"type note", service.VaultItemFilter{Type: "note"}, []string{"note"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ids(c.f); !slices.Equal(got, c.want) {
				t.Errorf("List = %v; want %v", got, c.want)
			}
		})
	}

	if _, err := items.List(ctx, "", service.VaultItemFilter{}); !errors.Is(err, service.ErrBadRequest) {
		t.Errorf("List(empty user) err = %v; want ErrBadRequest", err)
	}

	n, err := items.Count(ctx, "alice")
	if err != nil || n != 5 {
		t.Errorf("Count(alice) = %d, %v; want 5", n, err)
	}

	if err := items.DeleteAllByUser(ctx, "alice"); err != nil {
		t.Fatalf("DeleteAllByUser: %v", err)
	}
	if n, _ := items.Count(ctx, "alice"); n != 0 {
		t.Errorf("Count(alice) after DeleteAllByUser = %d", n)
	}
	if n, _ := items.Count(ctx, "bob"); n != 1 {
		t.Errorf("Count(bob) = %d; want 1", n)
	}
}
