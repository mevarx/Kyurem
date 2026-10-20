package proxy

import (
	"crypto/sha256"
	"encoding/hex"
)

// shortHash returns a stable 16-hex digest used for etag derivation.
func shortHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}
