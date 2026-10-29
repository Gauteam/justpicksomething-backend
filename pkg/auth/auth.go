// Package auth generates and checks room slugs, passcodes and member tokens.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	slugAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	slugLength   = 10

	// No 0/O, 1/I/L: passcodes get read aloud and typed on phones.
	passcodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	passcodeLength   = 6

	tokenBytes = 32
)

// NewSlug returns a random, URL-safe room identifier.
func NewSlug() string {
	return randomString(slugAlphabet, slugLength)
}

// NewPasscode returns a random passcode that is easy to read and type.
func NewPasscode() string {
	return randomString(passcodeAlphabet, passcodeLength)
}

// NormalizePasscode makes passcode comparison forgiving of case and stray
// spaces.
func NormalizePasscode(p string) string {
	return strings.ToUpper(strings.Join(strings.Fields(p), ""))
}

// HashPasscode hashes a passcode for storage.
func HashPasscode(passcode string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(NormalizePasscode(passcode)), bcrypt.DefaultCost)
	return string(h), err
}

// CheckPasscode reports whether passcode matches the stored hash.
func CheckPasscode(hash, passcode string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(NormalizePasscode(passcode))) == nil
}

// NewToken returns a random member bearer token and the hash to store.
func NewToken() (token string, hash []byte) {
	b := make([]byte, tokenBytes)
	rand.Read(b)
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

// HashToken returns the stored form of a bearer token. Tokens are high-entropy
// random values, so a fast hash is enough (unlike passcodes).
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// randomString picks n characters from alphabet using crypto/rand, without
// modulo bias.
func randomString(alphabet string, n int) string {
	limit := 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		rand.Read(buf)
		for _, b := range buf {
			if int(b) < limit && len(out) < n {
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	return string(out)
}
