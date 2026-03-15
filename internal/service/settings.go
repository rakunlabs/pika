package service

import (
	"context"
	"errors"
	"maps"

	"github.com/rakunlabs/pika/internal/external"
)

type Settings struct {
	External map[string]external.External `json:"external,omitempty"`
}

type PatchSettings struct {
	Action   ActionKey                    `json:"action"`
	External map[string]external.External `json:"external,omitempty"`
}

type ActionKey string

const (
	ActionKeySet    ActionKey = "set"
	ActionKeyRemove ActionKey = "remove"
)

func (s *Service) Settings(ctx context.Context) (*Settings, error) {
	keyPath := keySettings
	data, err := s.store.Get(ctx, keyPath)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Return empty settings on first use
			return &Settings{
				External: make(map[string]external.External),
			}, nil
		}
		return nil, err
	}

	var settings Settings
	if err := s.decodeBytes(data, &settings); err != nil {
		return nil, err
	}

	return &settings, nil
}

func (s *Service) PatchSettings(ctx context.Context, patch *PatchSettings) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}

	switch patch.Action {
	case ActionKeySet:
		if settings.External == nil {
			settings.External = make(map[string]external.External)
		}
		maps.Copy(settings.External, patch.External)
	case ActionKeyRemove:
		if settings.External != nil {
			for k := range patch.External {
				delete(settings.External, k)
			}
		}
	default:
		return ErrBadRequest
	}

	return s.UpdateSettings(ctx, settings)
}

func (s *Service) UpdateSettings(ctx context.Context, settings *Settings) error {
	keyPath := keySettings

	data, err := s.encodeBytes(settings)
	if err != nil {
		return err
	}

	if err := s.store.Set(ctx, keyPath, data); err != nil {
		return err
	}

	return nil
}
