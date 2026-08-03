package probot

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"strings"
)

// testPrivateKey is a generated RSA key so New() with AppID+PrivateKey works in
// tests without contacting GitHub.
var testPrivateKey = generateTestPrivateKey()

func generateTestPrivateKey() string {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return strings.TrimSpace(string(pem.EncodeToMemory(block)))
}

func osWriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func getenv(key string) string {
	return os.Getenv(key)
}
