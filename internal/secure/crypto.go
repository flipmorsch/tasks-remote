package secure

import (
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	SaltSize  = 16
	KeySize   = chacha20poly1305.KeySize
	NonceSize = chacha20poly1305.NonceSizeX
)

type KDFParams struct {
	Time    uint32
	Memory  uint32
	Threads uint8
}

func DefaultKDFParams() KDFParams {
	return KDFParams{
		Time:    3,
		Memory:  64 * 1024,
		Threads: 1,
	}
}

func RandomBytes(size int) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("random byte size must be positive")
	}
	out := make([]byte, size)
	if _, err := rand.Read(out); err != nil {
		return nil, fmt.Errorf("read random bytes: %w", err)
	}
	return out, nil
}

func DeriveKey(recoverySecret string, salt []byte, params KDFParams) ([]byte, error) {
	if recoverySecret == "" {
		return nil, errors.New("recovery secret is required")
	}
	if len(salt) != SaltSize {
		return nil, fmt.Errorf("salt must be %d bytes", SaltSize)
	}
	if params.Time == 0 || params.Memory == 0 || params.Threads == 0 {
		return nil, errors.New("invalid kdf parameters")
	}
	key := argon2.IDKey([]byte(recoverySecret), salt, params.Time, params.Memory, params.Threads, KeySize)
	return key, nil
}

func Seal(key, plaintext, associatedData []byte) (nonce []byte, ciphertext []byte, err error) {
	if len(key) != KeySize {
		return nil, nil, fmt.Errorf("key must be %d bytes", KeySize)
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, nil, fmt.Errorf("create aead: %w", err)
	}
	nonce, err = RandomBytes(NonceSize)
	if err != nil {
		return nil, nil, err
	}
	return nonce, aead.Seal(nil, nonce, plaintext, associatedData), nil
}

func Open(key, nonce, ciphertext, associatedData []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must be %d bytes", KeySize)
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("nonce must be %d bytes", NonceSize)
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("create aead: %w", err)
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, associatedData)
	if err != nil {
		return nil, errors.New("decrypt payload: authentication failed")
	}
	return plaintext, nil
}
