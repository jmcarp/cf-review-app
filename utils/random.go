package utils

import (
	"crypto/rand"
	"encoding/base64"
)

// SecureRandom generates a random string of `length` bytes
func SecureRandom(length int) (string, error) {
	b := make([]byte, length)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
