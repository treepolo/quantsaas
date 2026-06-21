package emergency

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

const (
	encryptedBundleVersion = 1
	encryptedBundleAlg     = "AES-256-GCM"
	encryptedBundleKDF     = "PBKDF2-SHA256"
	encryptedBundleRounds  = 310000
)

type EncryptedBundle struct {
	Version    int    `json:"version"`
	CreatedAt  string `json:"created_at"`
	Algorithm  string `json:"algorithm"`
	KDF        string `json:"kdf"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func EncryptFile(inPath string, outPath string, passphrase string) error {
	plain, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}
	encrypted, err := EncryptBytes(plain, passphrase)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(encrypted, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, append(raw, '\n'), 0o600)
}

func DecryptFile(inPath string, outPath string, passphrase string) error {
	raw, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}
	var encrypted EncryptedBundle
	if err := json.Unmarshal(raw, &encrypted); err != nil {
		return err
	}
	plain, err := DecryptBytes(encrypted, passphrase)
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, plain, 0o600)
}

func EncryptBytes(plain []byte, passphrase string) (EncryptedBundle, error) {
	if passphrase == "" {
		return EncryptedBundle{}, errors.New("passphrase is required")
	}
	salt, err := randomBytes(16)
	if err != nil {
		return EncryptedBundle{}, err
	}
	nonce, err := randomBytes(12)
	if err != nil {
		return EncryptedBundle{}, err
	}
	key := pbkdf2.Key([]byte(passphrase), salt, encryptedBundleRounds, 32, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return EncryptedBundle{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return EncryptedBundle{}, err
	}
	ciphertext := gcm.Seal(nil, nonce, plain, nil)
	return EncryptedBundle{
		Version:    encryptedBundleVersion,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Algorithm:  encryptedBundleAlg,
		KDF:        encryptedBundleKDF,
		Iterations: encryptedBundleRounds,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

func DecryptBytes(encrypted EncryptedBundle, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("passphrase is required")
	}
	if encrypted.Version != encryptedBundleVersion {
		return nil, fmt.Errorf("unsupported encrypted bundle version: %d", encrypted.Version)
	}
	if encrypted.Algorithm != encryptedBundleAlg || encrypted.KDF != encryptedBundleKDF {
		return nil, errors.New("unsupported encrypted bundle format")
	}
	if encrypted.Iterations <= 0 {
		return nil, errors.New("invalid encrypted bundle iterations")
	}
	salt, err := base64.StdEncoding.DecodeString(encrypted.Salt)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(encrypted.Nonce)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted.Ciphertext)
	if err != nil {
		return nil, err
	}
	key := pbkdf2.Key([]byte(passphrase), salt, encrypted.Iterations, 32, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("decrypt failed; passphrase may be wrong or file is corrupted")
	}
	return plain, nil
}

func randomBytes(size int) ([]byte, error) {
	out := make([]byte, size)
	if _, err := rand.Read(out); err != nil {
		return nil, err
	}
	return out, nil
}
