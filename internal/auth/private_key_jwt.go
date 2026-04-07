package auth

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const clientAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

func ClientAssertionType() string {
	return clientAssertionType
}

func RandomToken(size int) (string, error) {
	return randomToken(size)
}

type PrivateKeyJWTSigner struct {
	clientID string
	audience string
	signer   jose.Signer
}

func NewPrivateKeyJWTSigner(clientID string, audience string, privateKeyPath string, privateKeyID string) (*PrivateKeyJWTSigner, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, errors.New("missing client id")
	}
	if strings.TrimSpace(audience) == "" {
		return nil, errors.New("missing audience")
	}
	if strings.TrimSpace(privateKeyPath) == "" {
		return nil, errors.New("missing private key path")
	}

	privateKey, keyID, algorithm, err := loadPrivateKey(privateKeyPath, privateKeyID)
	if err != nil {
		return nil, err
	}

	signingKey := jose.SigningKey{
		Algorithm: algorithm,
		Key:       privateKey,
	}

	options := (&jose.SignerOptions{}).WithType("JWT")
	if keyID != "" {
		options.WithHeader("kid", keyID)
	}

	signer, err := jose.NewSigner(signingKey, options)
	if err != nil {
		return nil, err
	}

	return &PrivateKeyJWTSigner{
		clientID: clientID,
		audience: audience,
		signer:   signer,
	}, nil
}

func (signer *PrivateKeyJWTSigner) SignAssertion(now time.Time) (string, error) {
	jti, err := randomToken(32)
	if err != nil {
		return "", err
	}

	builder := jwt.Signed(signer.signer).Claims(jwt.Claims{
		Issuer:   signer.clientID,
		Subject:  signer.clientID,
		Audience: jwt.Audience{signer.audience},
		ID:       jti,
		IssuedAt: jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(
			now.Add(-1 * time.Minute),
		),
		Expiry: jwt.NewNumericDate(now.Add(5 * time.Minute)),
	})

	return builder.Serialize()
}

func loadPrivateKey(path string, keyIDOverride string) (any, string, jose.SignatureAlgorithm, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", "", err
	}

	keyMaterial := strings.TrimSpace(string(raw))
	keyID := strings.TrimSpace(keyIDOverride)
	if keyMaterial != "" && !strings.HasPrefix(keyMaterial, "-----BEGIN") {
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err == nil {
			if keyID == "" {
				keyID = firstString(payload, "keyId", "key_id", "kid")
			}

			if embeddedKey := firstString(payload, "privateKey", "private_key", "key"); embeddedKey != "" {
				keyMaterial = embeddedKey
			}
		}
	}

	privateKey, algorithm, err := parsePEMPrivateKey(keyMaterial)
	if err != nil {
		return nil, "", "", err
	}

	return privateKey, keyID, algorithm, nil
}

func parsePEMPrivateKey(raw string) (any, jose.SignatureAlgorithm, error) {
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, "", errors.New("invalid private key pem")
	}

	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return signatureAlgorithmForKey(key)
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return signatureAlgorithmForKey(key)
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return signatureAlgorithmForKey(key)
	}

	return nil, "", errors.New("unsupported private key format")
}

func signatureAlgorithmForKey(key any) (any, jose.SignatureAlgorithm, error) {
	switch key.(type) {
	case *rsa.PrivateKey:
		return key, jose.RS256, nil
	case *ecdsa.PrivateKey:
		return key, jose.ES256, nil
	default:
		return nil, "", fmt.Errorf("unsupported private key type %T", key)
	}
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}

		stringValue, ok := value.(string)
		if ok && strings.TrimSpace(stringValue) != "" {
			return strings.TrimSpace(stringValue)
		}
	}

	return ""
}

func randomToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
