package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const formatVersion byte = 1

var ErrUnavailable = errors.New("secret encryption is unavailable: MINICICD_MASTER_KEY is not configured")

type Box struct {
	aead cipher.AEAD
}

func New(key []byte) (*Box, error) {
	if len(key) == 0 {
		return &Box{}, nil
	}
	if len(key) != 32 {
		return nil, errors.New("master key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Available() bool { return b != nil && b.aead != nil }

func (b *Box) Encrypt(plain []byte, context string) ([]byte, error) {
	if !b.Available() {
		return nil, ErrUnavailable
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	header := make([]byte, 5)
	header[0] = formatVersion
	binary.BigEndian.PutUint32(header[1:], 1) // key version
	result := append(header, nonce...)
	result = b.aead.Seal(result, nonce, plain, []byte(context))
	return result, nil
}

func (b *Box) Decrypt(value []byte, context string) ([]byte, error) {
	if !b.Available() {
		return nil, ErrUnavailable
	}
	minimum := 5 + b.aead.NonceSize() + b.aead.Overhead()
	if len(value) < minimum || value[0] != formatVersion || binary.BigEndian.Uint32(value[1:5]) != 1 {
		return nil, errors.New("unsupported or invalid ciphertext")
	}
	nonce := value[5 : 5+b.aead.NonceSize()]
	plain, err := b.aead.Open(nil, nonce, value[5+b.aead.NonceSize():], []byte(context))
	if err != nil {
		return nil, errors.New("decrypt secret: authentication failed")
	}
	return plain, nil
}
