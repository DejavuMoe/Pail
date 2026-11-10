package app

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func lgErrorField(t *testing.T, kv []any) string {
	t.Helper()
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == "error" {
			return kv[i+1].(string)
		}
	}
	t.Fatal("no error field")
	return ""
}

// A registered secret that straddles the truncation point must not leave its
// first bytes in the log: the message is redacted before it is cut.
func TestLogTruncationNeverLeaksASecretPrefix(t *testing.T) {
	const secret = "STRADDLE-secret-value-0123456789"
	lgAddSecret(secret)
	msg := strings.Repeat("x", logMaxMessage-8) + secret + " tail"

	got := lgErrorField(t, lgErrFields(errors.New(msg)))
	if strings.Contains(got, "STRADDLE") || strings.Contains(got, secret) {
		t.Fatalf("lgErrFields leaked the secret prefix: ...%q", got[len(got)-30:])
	}

	s := lgLogOn(t)
	lgOpError(&OpError{Op: "Test", Kind: ErrOther, Message: msg}, 500)
	log := lgOff(t, s)
	if strings.Contains(log, "STRADDLE") || strings.Contains(log, secret) {
		t.Fatalf("lgOpError leaked the secret prefix:\n%s", log)
	}
	if !strings.Contains(log, "operation failed") {
		t.Fatalf("no record:\n%s", log)
	}
}

// A cut must not split a multi-byte character.
func TestLogTruncationKeepsValidUTF8(t *testing.T) {
	msg := "x" + strings.Repeat("€", 200) // byte 300 is inside a character
	got := lgErrorField(t, lgErrFields(errors.New(msg)))
	if !utf8.ValidString(got) {
		t.Fatalf("lgErrFields cut a character in two: %q", got[len(got)-8:])
	}
	if len(got) > logMaxMessage+len("...") || !strings.HasSuffix(got, "...") {
		t.Fatalf("length %d, suffix %q", len(got), got[len(got)-4:])
	}
	if short := lgErrorField(t, lgErrFields(errors.New("short"))); short != "short" {
		t.Fatalf("short message changed: %q", short)
	}
}
