// Package credentials coordinates provider-neutral application credential protection and storage.
package credentials

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// credentialProtectionPurposePrefix separates provider credential packages from other protected data.
	credentialProtectionPurposePrefix = "store_credential/"
)

// Metadata describes one durable credential revision without protected or plaintext payload bytes.
type Metadata struct {
	ProjectID     core.ProjectID        `json:"project_id"`
	ApplicationID core.ApplicationID    `json:"application_id"`
	Kind          stores.CredentialKind `json:"kind"`
	ContentType   string                `json:"content_type"`
	SchemaVersion int                   `json:"schema_version"`
	Revision      int64                 `json:"revision"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

// Service protects credential writes and resolves authenticated plaintext for provider adapters.
type Service struct {
	store             persistence.Store
	protectionService protection.Service
}

var (
	// credentialSourceContract verifies that Service implements the provider adapter credential port.
	_ stores.CredentialSource = (*Service)(nil)
)

// NewService validates and assembles application credential storage and protection dependencies.
func NewService(store persistence.Store, protectionService protection.Service) (*Service, error) {
	if store == nil || isNilDependency(store) {
		return nil, errors.New("credential persistence store is required")
	}
	if protectionService == nil || isNilDependency(protectionService) {
		return nil, errors.New("credential protection service is required")
	}
	return &Service{store: store, protectionService: protectionService}, nil
}

// Put creates or rotates one application credential with optimistic revision control.
func (service *Service) Put(
	ctx context.Context,
	application core.Application,
	credential stores.Credential,
	expectedRevision int64,
) (Metadata, error) {
	if err := errors.Join(application.Validate(), credential.Validate()); err != nil {
		return Metadata{}, err
	}
	if expectedRevision < 0 {
		return Metadata{}, errors.New("credential expected revision must not be negative")
	}

	plaintext := credential.Bytes()
	request, err := protection.NewRequest(credentialScope(
		application,
		credential.Kind,
		credential.ContentType,
		credential.SchemaVersion,
	), plaintext)
	zeroBytes(plaintext)
	if err != nil {
		return Metadata{}, err
	}
	protected, err := service.protectionService.Protect(ctx, request)
	if err != nil {
		return Metadata{}, fmt.Errorf("protect application credential: %w", err)
	}

	write := persistence.CredentialWrite{
		CredentialKey: persistence.CredentialKey{
			ProjectID:     application.ProjectID,
			ApplicationID: application.ID,
			Kind:          string(credential.Kind),
		},
		ContentType:      credential.ContentType,
		SchemaVersion:    credential.SchemaVersion,
		Payload:          protected,
		ExpectedRevision: expectedRevision,
	}
	var record persistence.CredentialRecord
	err = service.store.Transact(ctx, func(repository persistence.Transaction) error {
		authoritative, err := repository.Application(ctx, application.ProjectID, application.ID)
		if err != nil {
			return err
		}
		if authoritative != application {
			return fmt.Errorf("put application credential scope: %w", persistence.ErrConflict)
		}
		record, err = repository.PutCredential(ctx, write)
		return err
	})
	if err != nil {
		return Metadata{}, fmt.Errorf("put application credential: %w", err)
	}
	return metadataFromRecord(record), nil
}

// Credential resolves, authenticates, and opens one provider-owned credential package.
func (service *Service) Credential(
	ctx context.Context,
	application core.Application,
	kind stores.CredentialKind,
) (stores.Credential, error) {
	if err := errors.Join(application.Validate(), kind.Validate()); err != nil {
		return stores.Credential{}, err
	}

	key := persistence.CredentialKey{
		ProjectID:     application.ProjectID,
		ApplicationID: application.ID,
		Kind:          string(kind),
	}
	var record persistence.CredentialRecord
	err := service.store.Transact(ctx, func(repository persistence.Transaction) error {
		authoritative, err := repository.Application(ctx, application.ProjectID, application.ID)
		if err != nil {
			return err
		}
		if authoritative != application {
			return persistence.ErrNotFound
		}
		record, err = repository.Credential(ctx, key)
		return err
	})
	if err != nil {
		return stores.Credential{}, credentialPersistenceError("resolve application credential", err)
	}

	openRequest, err := protection.NewOpenRequest(credentialScope(
		application,
		kind,
		record.ContentType,
		record.SchemaVersion,
	), record.Payload)
	if err != nil {
		return stores.Credential{}, fmt.Errorf("prepare application credential opening: %w", err)
	}
	plaintext, err := service.protectionService.Open(ctx, openRequest)
	if err != nil {
		return stores.Credential{}, fmt.Errorf("open application credential: %w", err)
	}
	credential, credentialError := stores.NewCredential(
		kind,
		record.ContentType,
		record.SchemaVersion,
		plaintext,
	)
	zeroBytes(plaintext)
	if credentialError != nil {
		return stores.Credential{}, fmt.Errorf("validate opened application credential: %w", credentialError)
	}
	return credential, nil
}

// credentialScope binds one provider-owned kind to its project and application identity.
func credentialScope(
	application core.Application,
	kind stores.CredentialKind,
	contentType string,
	schemaVersion int,
) protection.Scope {
	return protection.Scope{
		ProjectID:     application.ProjectID,
		ApplicationID: application.ID,
		Purpose: credentialProtectionPurposePrefix + lengthPrefixedPurposeFields(
			string(application.Store.Provider),
			string(application.Store.Environment),
			string(application.Store.ID),
			string(kind),
			contentType,
			fmt.Sprint(schemaVersion),
		),
	}
}

// lengthPrefixedPurposeFields encodes authenticated metadata without delimiter ambiguity.
func lengthPrefixedPurposeFields(fields ...string) string {
	var purpose string
	for _, field := range fields {
		purpose += fmt.Sprintf("%d:%s", len(field), field)
	}
	return purpose
}

// metadataFromRecord removes protected bytes from one durable credential result.
func metadataFromRecord(record persistence.CredentialRecord) Metadata {
	return Metadata{
		ProjectID:     record.ProjectID,
		ApplicationID: record.ApplicationID,
		Kind:          stores.CredentialKind(record.Kind),
		ContentType:   record.ContentType,
		SchemaVersion: record.SchemaVersion,
		Revision:      record.Revision,
		CreatedAt:     record.CreatedAt,
		UpdatedAt:     record.UpdatedAt,
	}
}

// credentialPersistenceError maps durable absence to the adapter-facing credential contract.
func credentialPersistenceError(operation string, err error) error {
	if errors.Is(err, persistence.ErrNotFound) {
		return fmt.Errorf("%s: %w", operation, stores.ErrCredentialNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// isNilDependency detects typed nil values stored behind service interfaces.
func isNilDependency(dependency any) bool {
	value := reflect.ValueOf(dependency)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// zeroBytes overwrites one temporary plaintext copy after crossing a credential boundary.
func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
