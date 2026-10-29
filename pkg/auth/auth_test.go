package auth

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewSlug(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		s := NewSlug()
		if len(s) != slugLength {
			t.Fatalf("slug %q has length %d, want %d", s, len(s), slugLength)
		}
		for _, c := range s {
			if !strings.ContainsRune(slugAlphabet, c) {
				t.Fatalf("slug %q contains %q", s, c)
			}
		}
		if seen[s] {
			t.Fatalf("duplicate slug %q", s)
		}
		seen[s] = true
	}
}

func TestNewPasscode(t *testing.T) {
	for range 1000 {
		p := NewPasscode()
		if len(p) != passcodeLength {
			t.Fatalf("passcode %q has length %d, want %d", p, len(p), passcodeLength)
		}
		if strings.ContainsAny(p, "0O1IL") {
			t.Fatalf("passcode %q contains an ambiguous character", p)
		}
	}
}

func TestPasscodeHashing(t *testing.T) {
	hash, err := HashPasscode("ABC234")
	if err != nil {
		t.Fatal(err)
	}

	for _, attempt := range []string{"ABC234", "abc234", " abc 234 "} {
		if !CheckPasscode(hash, attempt) {
			t.Errorf("CheckPasscode(%q) = false, want true", attempt)
		}
	}
	for _, attempt := range []string{"", "ABC235", "ABC2345"} {
		if CheckPasscode(hash, attempt) {
			t.Errorf("CheckPasscode(%q) = true, want false", attempt)
		}
	}
}

func TestNewToken(t *testing.T) {
	token, hash := NewToken()
	if len(token) < 40 {
		t.Errorf("token %q is too short", token)
	}
	if !bytes.Equal(hash, HashToken(token)) {
		t.Error("returned hash doesn't match HashToken(token)")
	}

	other, _ := NewToken()
	if other == token {
		t.Error("two tokens are equal")
	}
}
