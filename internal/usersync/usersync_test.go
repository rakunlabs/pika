package usersync

import (
	"reflect"
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

func TestGroupMemberKeyExtractsUIDFromDN(t *testing.T) {
	tests := []struct {
		name string
		in   string
		attr string
		want string
	}{
		{name: "uid dn", in: "uid=alice,ou=Users,dc=example,dc=com", attr: "uid", want: "alice"},
		{name: "case insensitive", in: "UID=Bob,ou=Users,dc=example,dc=com", attr: "uid", want: "Bob"},
		{name: "plain value", in: "carol", attr: "uid", want: "carol"},
		{name: "full dn", in: "cn=Carol Smith,ou=Users,dc=example,dc=com", attr: "dn", want: "cn=Carol Smith,ou=Users,dc=example,dc=com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := groupMemberKey(tt.in, tt.attr); got != tt.want {
				t.Fatalf("groupMemberKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGroupsForEntryMergesDirectAndGroupSearchMemberships(t *testing.T) {
	idx := groupIndex{}
	idx.add("alice", "pika-admins")
	idx.add("alice", "pika-editors")

	entry := adaEntry{
		DN: "uid=alice,ou=Users,dc=example,dc=com",
		Attributes: map[string][]string{
			"uid":      {"alice"},
			"memberOf": {"cn=legacy,ou=groups,dc=example,dc=com", "pika-admins"},
		},
	}
	groups := groupsForEntry(entry, service.LDAPAttributeMap{Username: "uid", Groups: "memberOf"}, idx)
	want := []string{"cn=legacy,ou=groups,dc=example,dc=com", "pika-admins", "pika-editors"}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("groups = %#v, want %#v", groups, want)
	}
}

func TestLoginUserFilterCombinesBaseFilterAndIdentifiers(t *testing.T) {
	filter, err := loginUserFilter("(objectClass=person)", service.LDAPAttributeMap{Username: "uid", Email: "mail"}, service.ExternalIdentityInput{
		Subject: "alice*(test)",
		Email:   "alice@example.com",
	})
	if err != nil {
		t.Fatalf("loginUserFilter: %v", err)
	}
	want := "(&(objectClass=person)(|(uid=alice\\2a\\28test\\29)(mail=alice@example.com)))"
	if filter != want {
		t.Fatalf("filter = %q, want %q", filter, want)
	}
}

func TestLDAPLoginSyncSource(t *testing.T) {
	auth := &service.AuthSettings{LDAP: &service.LDAPStrategySettings{
		Name:           "corp-ldap",
		AutoCreateUser: true,
		UserSyncSource: "ldap-prod",
	}}
	sourceID, autoCreate, ok := auth.LDAPLoginSyncSource("corp-ldap")
	if !ok || !autoCreate || sourceID != "ldap-prod" {
		t.Fatalf("LDAPLoginSyncSource = %q, %v, %v; want ldap-prod, true, true", sourceID, autoCreate, ok)
	}
	if _, _, ok := auth.LDAPLoginSyncSource("other"); ok {
		t.Fatal("unexpected match for other provider")
	}
}
