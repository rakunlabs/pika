package service_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"sort"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

func zipContents(t *testing.T, buf *bytes.Buffer) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("opening zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestVaultFilesZipFolder(t *testing.T) {
	svc, _ := newVaultFilesService(t)
	ctx := t.Context()

	a := upload(t, svc, "u1", "", "docs/deep", "a.txt", "aaa")
	upload(t, svc, "u1", "", "docs", "b.txt", "bbb")
	upload(t, svc, "u1", "", "", "outside.txt", "nope")
	if _, err := svc.CreateVaultFolder(ctx, "u1", "", "docs/empty"); err != nil {
		t.Fatal(err)
	}
	deep, _ := svc.GetVaultFile(ctx, "u1", a.ParentID)
	docs := deep.ParentID

	z, err := svc.PrepareVaultZip(ctx, "u1", docs)
	if err != nil {
		t.Fatal(err)
	}
	if z.Name != "docs" {
		t.Fatalf("name %q", z.Name)
	}
	var buf bytes.Buffer
	stats, err := z.WriteTo(ctx, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Files != 2 || stats.Failed != 0 {
		t.Fatalf("stats %+v", stats)
	}
	got := zipContents(t, &buf)
	want := []string{"docs/", "docs/b.txt", "docs/deep/", "docs/deep/a.txt", "docs/empty/"}
	if k := keys(got); len(k) != len(want) {
		t.Fatalf("entries %v, want %v", k, want)
	}
	for _, w := range want {
		if _, ok := got[w]; !ok {
			t.Fatalf("missing %s in %v", w, keys(got))
		}
	}
	if got["docs/deep/a.txt"] != "aaa" || got["docs/b.txt"] != "bbb" {
		t.Fatalf("content mismatch: %v", got)
	}

	// Root download includes everything, without a wrapping folder.
	z, err = svc.PrepareVaultZip(ctx, "u1", "")
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if _, err := z.WriteTo(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	if got := zipContents(t, &buf); got["outside.txt"] != "nope" || got["docs/deep/a.txt"] != "aaa" {
		t.Fatalf("root zip: %v", keys(got))
	}

	// Files and other users' folders are rejected before streaming.
	if _, err := svc.PrepareVaultZip(ctx, "u1", a.ID); !errors.Is(err, service.ErrBadRequest) {
		t.Fatalf("file zip: %v", err)
	}
	if _, err := svc.PrepareVaultZip(ctx, "u2", docs); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("cross-user zip: %v", err)
	}
}
