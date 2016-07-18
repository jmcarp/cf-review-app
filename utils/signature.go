package utils

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

func CheckSignature(key, message []byte, signature string) bool {
	h := hmac.New(sha1.New, key)
	h.Write(message)
	digest := hex.EncodeToString(h.Sum(nil))
	calculated := fmt.Sprintf("sha1=%s", digest)
	return subtle.ConstantTimeCompare([]byte(signature), []byte(calculated)) == 1
}
