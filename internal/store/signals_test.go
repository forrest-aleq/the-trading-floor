package store

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifySignalWriteErrorMapsDuplicateContentHash(t *testing.T) {
	err := classifySignalWriteError(&pgconn.PgError{
		Code:           "23505",
		ConstraintName: "uq_signals_content_hash",
	})
	if !errors.Is(err, ErrDuplicateSignalContentHash) {
		t.Fatalf("expected duplicate content hash error, got %v", err)
	}
}

func TestClassifySignalWriteErrorLeavesOtherErrorsUntouched(t *testing.T) {
	original := &pgconn.PgError{Code: "23505", ConstraintName: "other_constraint"}
	if got := classifySignalWriteError(original); got != original {
		t.Fatalf("expected original error, got %v", got)
	}
}
