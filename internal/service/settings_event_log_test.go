package service_test

import (
	"testing"

	"github.com/rakunlabs/pika/internal/service"
)

func TestEventLogSettingPersists(t *testing.T) {
	store := newTokenTestStore(t)
	svc := newServiceOn(t, store)
	ctx := t.Context()

	if err := svc.PatchSettings(ctx, &service.PatchSettings{
		Action:   service.ActionKeySet,
		EventLog: &service.EventLogSettings{Disabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	// A fresh service on the same store simulates a restart.
	restarted := newServiceOn(t, store)
	settings, err := restarted.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.EventLogEnabled() {
		t.Fatal("event log disabled flag was not persisted")
	}
}
