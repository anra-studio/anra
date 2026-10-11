package a2a

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

func fingerprint(encoded []byte) string {
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// Hash the same JSON array representation as before, using the JSON bytes
// already produced for each entry's size validation. No second serialization
// or allocation of the complete array is needed.
var fingerprintDelimiters = [...]byte{'[', ',', ']'}

type entryFingerprint struct {
	digest hash.Hash
	first  bool
}

func newEntryFingerprint() entryFingerprint {
	digest := sha256.New()
	digest.Write(fingerprintDelimiters[:1])
	return entryFingerprint{digest: digest, first: true}
}

func (f *entryFingerprint) add(encoded []byte) {
	if !f.first {
		f.digest.Write(fingerprintDelimiters[1:2])
	}
	f.digest.Write(encoded)
	f.first = false
}

func (f *entryFingerprint) sum() string {
	f.digest.Write(fingerprintDelimiters[2:])
	return hex.EncodeToString(f.digest.Sum(nil))
}
