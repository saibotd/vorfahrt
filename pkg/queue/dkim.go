package queue

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"log"
	"os"
)

func LoadOrGenerateDKIMKey(path string) (*rsa.PrivateKey, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		log.Println("No DKIM key found. Generating a new 2048-bit RSA key...")
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, err
		}

		keyBytes := x509.MarshalPKCS1PrivateKey(key)
		pemBlock := &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: keyBytes,
		}

		file, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		if err := pem.Encode(file, pemBlock); err != nil {
			return nil, err
		}

		pubKeyBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
		if err == nil {
			pubKeyBase64 := base64.StdEncoding.EncodeToString(pubKeyBytes)
			log.Println("==================================================================")
			log.Println("DKIM Key generated successfully!")
			log.Println("Please add the following TXT record to your domain's DNS:")
			log.Println("")
			log.Printf("Name: mail._domainkey")
			log.Printf("Value: v=DKIM1; k=rsa; p=%s", pubKeyBase64)
			log.Println("==================================================================")
		}

		return key, nil
	}

	keyData, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, os.ErrInvalid
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// try pkcs8
		pkcs8Key, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 == nil {
			if rsaKey, ok := pkcs8Key.(*rsa.PrivateKey); ok {
				return rsaKey, nil
			}
		}
		return nil, err
	}

	return key, nil
}
