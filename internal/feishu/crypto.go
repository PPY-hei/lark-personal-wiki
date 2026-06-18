package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

func DecryptEvent(encrypted string, key string) ([]byte, error) {
	cipherText, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	hash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}
	if len(cipherText) < aes.BlockSize || len(cipherText)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid cipher text length")
	}

	iv := cipherText[:aes.BlockSize]
	payload := cipherText[aes.BlockSize:]
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(payload, payload)

	plain, err := pkcs7Unpad(payload, aes.BlockSize)
	if err != nil {
		return nil, err
	}
	return plain, nil
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("invalid pkcs7 data")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, fmt.Errorf("invalid pkcs7 padding")
	}
	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return nil, fmt.Errorf("invalid pkcs7 padding bytes")
		}
	}
	return data[:len(data)-padding], nil
}
