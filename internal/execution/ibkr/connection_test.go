package ibkr

import "testing"

func TestIsClientIDConflict(t *testing.T) {
	if !isClientIDConflict(assertErr("Unable to connect as the client id is already in use. Retry with a unique client id.")) {
		t.Fatal("expected client id conflict to be detected")
	}
	if isClientIDConflict(assertErr("some other gateway problem")) {
		t.Fatal("did not expect unrelated error to be treated as client id conflict")
	}
}

type staticErr string

func (e staticErr) Error() string { return string(e) }

func assertErr(message string) error { return staticErr(message) }
