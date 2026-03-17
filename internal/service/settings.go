package service

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/rakunlabs/pika/internal/external"
	"golang.org/x/crypto/bcrypt"
)

type Settings struct {
	External        map[string]external.External `json:"external,omitempty"`
	AdminSecretHash string                       `json:"admin_secret_hash,omitempty"`
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
	settings, err := s.store.Settings().Get(ctx)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Return empty settings on first use
			return &Settings{
				External: make(map[string]external.External),
			}, nil
		}
		return nil, err
	}

	return settings, nil
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
	return s.store.Settings().Set(ctx, settings)
}

// SetAdminSecret hashes the provided plaintext secret with bcrypt and stores it in settings.
// If a current secret is already set, currentSecret must match it.
func (s *Service) SetAdminSecret(ctx context.Context, currentSecret, newSecret string) error {
	if newSecret == "" {
		return fmt.Errorf("new secret is required: %w", ErrBadRequest)
	}

	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}

	// If an admin secret is already configured, validate the current one.
	if settings.AdminSecretHash != "" {
		if currentSecret == "" {
			return fmt.Errorf("current secret is required: %w", ErrBadRequest)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(settings.AdminSecretHash), []byte(currentSecret)); err != nil {
			return fmt.Errorf("invalid current secret: %w", ErrForbidden)
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newSecret), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing admin secret: %w", err)
	}

	settings.AdminSecretHash = string(hash)

	return s.UpdateSettings(ctx, settings)
}

// VerifyAdminSecret checks the provided plaintext against the stored bcrypt hash.
// Returns ErrForbidden if the secret does not match, or ErrBadRequest if no secret is configured.
func (s *Service) VerifyAdminSecret(ctx context.Context, secret string) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}

	if settings.AdminSecretHash == "" {
		return fmt.Errorf("admin secret is not configured: %w", ErrBadRequest)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(settings.AdminSecretHash), []byte(secret)); err != nil {
		return fmt.Errorf("invalid admin secret: %w", ErrForbidden)
	}

	return nil
}

// HasAdminSecret returns true if an admin secret has been configured.
func (s *Service) HasAdminSecret(ctx context.Context) (bool, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return false, err
	}

	return settings.AdminSecretHash != "", nil
}
