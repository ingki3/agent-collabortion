package client

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
)

// IdempotencyNamespace is the fixed UUIDv5 namespace for CLI idempotency keys
// (colab-cli.md §1 "UUIDv5(namespace=colab, …)"). It is itself
// UUIDv5(NameSpace_DNS, "colab") so any implementation can re-derive it; it
// must never change — the key for (task, seq) has to be stable across
// attempts, hosts and CLI versions or E8-04 replay breaks.
const IdempotencyNamespace = "454e4096-cb98-57f5-b314-6c5499b55cc8"

// nameSpaceDNS is RFC 4122 NameSpace_DNS, used once to derive IdempotencyNamespace.
const nameSpaceDNS = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

// UUIDv5 returns the RFC 4122 name-based (SHA-1) UUID for name under namespace.
func UUIDv5(namespace, name string) (string, error) {
	ns, err := parseUUID(namespace)
	if err != nil {
		return "", err
	}
	h := sha1.New()
	h.Write(ns[:])
	h.Write([]byte(name))
	sum := h.Sum(nil)
	var u [16]byte
	copy(u[:], sum[:16])
	u[6] = (u[6] & 0x0f) | 0x50 // version 5
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 4122 variant
	return formatUUID(u), nil
}

func parseUUID(s string) ([16]byte, error) {
	var u [16]byte
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return u, fmt.Errorf("invalid uuid %q", s)
	}
	hexs := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:]
	b, err := hex.DecodeString(hexs)
	if err != nil {
		return u, fmt.Errorf("invalid uuid %q: %v", s, err)
	}
	copy(u[:], b)
	return u, nil
}

func formatUUID(u [16]byte) string {
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
