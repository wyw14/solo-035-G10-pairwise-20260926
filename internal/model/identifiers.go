package model

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type EventID string
type RevisionID string
type CommandID string
type LeaseID string
type SourceID string
type SlateID string

func NewEventID() EventID {
	return EventID(uuid.NewString())
}

func NewRevisionID() RevisionID {
	return RevisionID(uuid.NewString())
}

func NewCommandID() CommandID {
	return CommandID(uuid.NewString())
}

func NewLeaseID() LeaseID {
	return LeaseID(uuid.NewString())
}

func NewSourceID() SourceID {
	return SourceID(uuid.NewString())
}

func NewSlateID() SlateID {
	return SlateID(uuid.NewString())
}

func ValidateIdentity(value string, kind string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s identity is empty", kind)
	}
	if _, err := uuid.Parse(trimmed); err != nil {
		return fmt.Errorf("%s identity is invalid: %w", kind, err)
	}
	return nil
}

func (id EventID) Valid() bool {
	return ValidateIdentity(string(id), "event") == nil
}

func (id RevisionID) Valid() bool {
	return ValidateIdentity(string(id), "revision") == nil
}

func (id CommandID) Valid() bool {
	return ValidateIdentity(string(id), "command") == nil
}

func (id LeaseID) Valid() bool {
	return ValidateIdentity(string(id), "lease") == nil
}

func (id SourceID) Valid() bool {
	return ValidateIdentity(string(id), "source") == nil
}
