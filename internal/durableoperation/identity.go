// Package durableoperation provides the storage-free identity and replay
// comparison shared by a deliberately small set of durable operation pilots.
//
// It does not own transactions, persistence, authority, leases, receipts,
// recovery, cleanup, or domain state machines.
package durableoperation

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

const MaxDomainSeparatorBytes = 128

var versionedDomainSeparator = regexp.MustCompile(
	`^[a-z][a-z0-9]*([._-][a-z0-9]+)*\.v(0|[1-9][0-9]*)$`,
)

// Identity is the normalized, immutable identity subset common to the pilot
// ledgers. Domain services retain every other request, result, and authority
// binding.
type Identity struct {
	domainSeparator    string
	operationKeyDigest string
	requestFingerprint string
}

func NewIdentity(domainSeparator string, operationKeyDigest string,
	requestFingerprint string,
) (Identity, error) {
	identity := Identity{
		domainSeparator:    domainSeparator,
		operationKeyDigest: operationKeyDigest,
		requestFingerprint: requestFingerprint,
	}
	if err := identity.Validate(); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func (i Identity) Validate() error {
	if !ValidDomainSeparator(i.domainSeparator) {
		return errors.New("durable operation domain separator must be normalized and versioned")
	}
	if !validDigest(i.operationKeyDigest) || !validDigest(i.requestFingerprint) {
		return errors.New("durable operation identity requires lowercase SHA-256 digests")
	}
	return nil
}

func (i Identity) DomainSeparator() string {
	return i.domainSeparator
}

func (i Identity) OperationKeyDigest() string {
	return i.operationKeyDigest
}

func (i Identity) RequestFingerprint() string {
	return i.requestFingerprint
}

func ValidDomainSeparator(value string) bool {
	return value != "" && len(value) <= MaxDomainSeparatorBytes &&
		utf8.ValidString(value) && versionedDomainSeparator.MatchString(value)
}

// Fingerprint hashes a versioned domain separator and ordered semantic fields.
// Every component is prefixed by its unsigned 64-bit big-endian byte length, so
// field boundaries, empty fields, and field order cannot collapse into raw
// string-concatenation ambiguity.
func Fingerprint(domainSeparator string, fields ...string) (string, error) {
	if !ValidDomainSeparator(domainSeparator) {
		return "", errors.New("durable operation fingerprint domain must be normalized and versioned")
	}
	for _, field := range fields {
		if !utf8.ValidString(field) {
			return "", errors.New("durable operation fingerprint fields must be valid UTF-8")
		}
	}
	hash := sha256.New()
	var size [8]byte
	writePart := func(part string) {
		binary.BigEndian.PutUint64(size[:], uint64(len([]byte(part))))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(part))
	}
	writePart(domainSeparator)
	for _, field := range fields {
		writePart(field)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type ReplayDecision uint8

const (
	DecisionReplay ReplayDecision = iota + 1
	DecisionConflict
)

func (d ReplayDecision) String() string {
	switch d {
	case DecisionReplay:
		return "replay"
	case DecisionConflict:
		return "conflict"
	default:
		return "invalid"
	}
}

// Decide compares two identities already selected under one durable operation
// key. A changed request is a conflict. Malformed identities, different domains,
// and different keys fail closed because they are not comparable retries.
func Decide(stored Identity, requested Identity) (ReplayDecision, error) {
	if err := stored.Validate(); err != nil {
		return 0, fmt.Errorf("stored durable operation identity is invalid: %w", err)
	}
	if err := requested.Validate(); err != nil {
		return 0, fmt.Errorf("requested durable operation identity is invalid: %w", err)
	}
	if stored.domainSeparator != requested.domainSeparator {
		return 0, errors.New("durable operation identity domains differ")
	}
	if stored.operationKeyDigest != requested.operationKeyDigest {
		return 0, errors.New("durable operation key digests differ")
	}
	if stored.requestFingerprint == requested.requestFingerprint {
		return DecisionReplay, nil
	}
	return DecisionConflict, nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == hex.EncodeToString(decoded)
}
