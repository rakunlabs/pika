package bw

import (
	"context"
	"testing"
	"time"

	"github.com/rakunlabs/bw"
	"github.com/rakunlabs/query"
)

// userRowV1 mirrors the users bucket schema BEFORE the DeniedCaps field was
// added (bucket version 1). Used to seed a "legacy" DB so the test can prove
// the v1→v2 bump migrates without losing data.
type userRowV1 struct {
	ID           string      `bw:"id,pk"`
	Username     string      `bw:"username,unique"`
	Email        string      `bw:"email,index"`
	PasswordHash string      `bw:"password_hash"`
	DisplayName  string      `bw:"display_name"`
	External     bool        `bw:"external"`
	Disabled     bool        `bw:"disabled"`
	IsSuperadmin bool        `bw:"is_superadmin"`
	CreatedAt    time.Time   `bw:"created_at,index"`
	UpdatedAt    time.Time   `bw:"updated_at"`
	Grants       []userGrant `bw:"grants"`
}

// TestMigrateUsersV1ToV2_PreservesRows reproduces the "schema fingerprint
// mismatch" upgrade scenario: an existing DB whose users bucket was written at
// v1 is reopened with the current (v2) schema that adds DeniedCaps. The bump
// must migrate in place — every user row keeps its fields and grants, and the
// new field decodes as nil (no denials).
func TestMigrateUsersV1ToV2_PreservesRows(t *testing.T) {
	db, err := bw.Open("", bw.WithInMemory(true))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// 1. Seed a legacy v1 bucket with a real user (incl. a grant).
	b1, err := bw.RegisterBucket[userRowV1](db, bucketUsers, bw.WithVersion[userRowV1](1))
	if err != nil {
		t.Fatalf("register v1: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := b1.Insert(ctx, &userRowV1{
		ID:           "u1",
		Username:     "alice",
		Email:        "alice@example.com",
		IsSuperadmin: true,
		CreatedAt:    now,
		UpdatedAt:    now,
		Grants:       []userGrant{{PermissionID: "p1", Source: "local"}},
	}); err != nil {
		t.Fatalf("insert v1 user: %v", err)
	}

	// 2. Reopen the SAME bucket at v2 with the current schema. This is the
	// exact path that errored before the version bump; it must now migrate.
	b2, err := bw.RegisterBucket[userRow](db, bucketUsers, bw.WithVersion[userRow](2))
	if err != nil {
		t.Fatalf("register v2 (migration must not error): %v", err)
	}

	// 3. The legacy row must survive intact, with DeniedCaps defaulting nil.
	got, err := b2.Get(ctx, "u1")
	if err != nil {
		t.Fatalf("get after migration: %v", err)
	}
	if got.Username != "alice" || got.Email != "alice@example.com" || !got.IsSuperadmin {
		t.Errorf("user fields not preserved across migration: %+v", got)
	}
	if len(got.Grants) != 1 || got.Grants[0].PermissionID != "p1" || got.Grants[0].Source != "local" {
		t.Errorf("grants not preserved across migration: %+v", got.Grants)
	}
	if got.DeniedCaps != nil {
		t.Errorf("new DeniedCaps field should default to nil, got %v", got.DeniedCaps)
	}

	// 4. The unique username index must still work after migration.
	q, err := query.Parse("username=alice")
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	rows, err := b2.Find(ctx, q)
	if err != nil {
		t.Fatalf("find by username after migration: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "u1" {
		t.Errorf("username index broken after migration: %+v", rows)
	}
}
