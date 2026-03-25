package protocol

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// QueryEncoding controls how DNS query subdomains are encoded.
type QueryEncoding int

const (
	// QuerySingleLabel uses base32 in a single DNS label (default, stealthier).
	QuerySingleLabel QueryEncoding = iota
	// QueryDoubleLabel uses hex split across two DNS labels.
	QueryDoubleLabel
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// EncodeQuery creates an encrypted DNS query subdomain.
// Single-label (default): [base32_encrypted].domain
// Double-label:           [hex_part1].[hex_part2].domain
// Payload: 4 random + 2 channel + 2 block = 8 bytes, encrypted with AES-GCM.
func EncodeQuery(queryKey [KeySize]byte, channel, block uint16, domain string, mode QueryEncoding) (string, error) {
	payload := make([]byte, QueryPayloadSize)

	if _, err := rand.Read(payload[:QueryPaddingSize]); err != nil {
		return "", fmt.Errorf("random padding: %w", err)
	}

	binary.BigEndian.PutUint16(payload[QueryPaddingSize:], channel)
	binary.BigEndian.PutUint16(payload[QueryPaddingSize+QueryChannelSize:], block)

	encrypted, err := Encrypt(queryKey, payload)
	if err != nil {
		return "", fmt.Errorf("encrypt query: %w", err)
	}

	switch mode {
	case QueryDoubleLabel:
		h := hex.EncodeToString(encrypted)
		mid := len(h) / 2
		return fmt.Sprintf("%s.%s.%s", h[:mid], h[mid:], domain), nil
	default:
		encoded := strings.ToLower(b32.EncodeToString(encrypted))
		return fmt.Sprintf("%s.%s", encoded, domain), nil
	}
}

// DecodeQuery parses and decrypts a DNS query subdomain.
// Auto-detects single-label (base32) or double-label (hex) encoding.
func DecodeQuery(queryKey [KeySize]byte, qname, domain string) (channel, block uint16, err error) {
	qname = strings.TrimSuffix(qname, ".")
	domain = strings.TrimSuffix(domain, ".")

	suffix := "." + domain
	if !strings.HasSuffix(strings.ToLower(qname), strings.ToLower(suffix)) {
		return 0, 0, fmt.Errorf("domain mismatch: %q does not end with %q", qname, suffix)
	}

	encoded := qname[:len(qname)-len(suffix)]

	// Try base32 first (single label, no dots, or dots stripped)
	b32str := strings.ReplaceAll(encoded, ".", "")
	ciphertext, err := b32.DecodeString(strings.ToUpper(b32str))
	if err == nil {
		return decryptQuery(queryKey, ciphertext)
	}

	// Fall back to hex (double-label)
	hexStr := strings.ReplaceAll(encoded, ".", "")
	ciphertext, err = hex.DecodeString(hexStr)
	if err != nil {
		return 0, 0, fmt.Errorf("decode query: invalid encoding")
	}
	return decryptQuery(queryKey, ciphertext)
}

func decryptQuery(queryKey [KeySize]byte, ciphertext []byte) (channel, block uint16, err error) {
	plaintext, err := Decrypt(queryKey, ciphertext)
	if err != nil {
		return 0, 0, fmt.Errorf("decrypt: %w", err)
	}

	if len(plaintext) != QueryPayloadSize {
		return 0, 0, fmt.Errorf("invalid payload size: %d", len(plaintext))
	}

	channel = binary.BigEndian.Uint16(plaintext[QueryPaddingSize:])
	block = binary.BigEndian.Uint16(plaintext[QueryPaddingSize+QueryChannelSize:])
	return channel, block, nil
}

// EncodeResponse encrypts and base64-encodes a block payload for a DNS TXT response.
// Adds a 2-byte length prefix and random padding to vary response size for anti-DPI.
func EncodeResponse(responseKey [KeySize]byte, data []byte, maxPadding int) (string, error) {
	padLen := 0
	if maxPadding > 0 {
		buf := make([]byte, 1)
		rand.Read(buf)
		padLen = int(buf[0]) % (maxPadding + 1)
	}

	padded := make([]byte, PadLengthSize+len(data)+padLen)
	binary.BigEndian.PutUint16(padded, uint16(len(data)))
	copy(padded[PadLengthSize:], data)
	if padLen > 0 {
		rand.Read(padded[PadLengthSize+len(data):])
	}

	encrypted, err := Encrypt(responseKey, padded)
	if err != nil {
		return "", fmt.Errorf("encrypt response: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// DecodeResponse base64-decodes and decrypts a DNS TXT response, stripping padding.
func DecodeResponse(responseKey [KeySize]byte, encoded string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	padded, err := Decrypt(responseKey, ciphertext)
	if err != nil {
		return nil, err
	}
	if len(padded) < PadLengthSize {
		return nil, fmt.Errorf("response too short")
	}
	dataLen := int(binary.BigEndian.Uint16(padded))
	if dataLen > len(padded)-PadLengthSize {
		return nil, fmt.Errorf("invalid data length in response: %d", dataLen)
	}
	return padded[PadLengthSize : PadLengthSize+dataLen], nil
}
