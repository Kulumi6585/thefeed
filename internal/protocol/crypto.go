package protocol

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	KeySize   = 32 // AES-256
	NonceSize = 12 // GCM nonce
)

// DeriveKeys derives separate query and response AES-256 keys from a passphrase using HKDF.
func DeriveKeys(passphrase string) (queryKey, responseKey [KeySize]byte, err error) {
	master := sha256.Sum256([]byte(passphrase))

	qr := hkdf.New(sha256.New, master[:], nil, []byte("thefeed-query"))
	if _, err = io.ReadFull(qr, queryKey[:]); err != nil {
		return
	}

	rr := hkdf.New(sha256.New, master[:], nil, []byte("thefeed-response"))
	_, err = io.ReadFull(rr, responseKey[:])
	return
}

func newGCM(key [KeySize]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt encrypts plaintext using AES-256-GCM. Returns nonce+ciphertext+tag.
func Encrypt(key [KeySize]byte, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts AES-256-GCM ciphertext (nonce+ciphertext+tag).
func Decrypt(key [KeySize]byte, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	if len(ciphertext) < gcm.NonceSize()+gcm.Overhead() {
		return nil, fmt.Errorf("ciphertext too short: %d bytes", len(ciphertext))
	}

	nonce := ciphertext[:gcm.NonceSize()]
	return gcm.Open(nil, nonce, ciphertext[gcm.NonceSize():], nil)
}
