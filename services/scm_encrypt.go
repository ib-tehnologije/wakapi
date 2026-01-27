package services

import (
	"crypto/sha512"

	"github.com/gorilla/securecookie"
	"github.com/muety/wakapi/config"
)

// TokenCipher provides basic symmetric encryption for secrets at rest.
type TokenCipher interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}

type secureCookieCipher struct {
	secures []*securecookie.SecureCookie
}

func (c *secureCookieCipher) Encrypt(value string) (string, error) {
	encoded, err := c.secures[0].Encode("token", value)
	if err != nil {
		return "", err
	}
	return encoded, nil
}

func (c *secureCookieCipher) Decrypt(value string) (string, error) {
	var decoded string
	for _, sc := range c.secures {
		if err := sc.Decode("token", value, &decoded); err == nil {
			return decoded, nil
		}
	}
	return "", securecookie.ErrMacInvalid
}

func newTokenCipher() TokenCipher {
	cfg := config.Get()

	secures := []*securecookie.SecureCookie{}

	// Prefer deterministic key derived from password_salt, so tokens survive restarts.
	if cfg.Security.PasswordSalt != "" {
		hash := sha512.Sum512([]byte(cfg.Security.PasswordSalt))
		hashKey := hash[:32]
		blockKey := hash[32:]
		secures = append(secures, securecookie.New(hashKey, blockKey))
	}

	// Fallback to runtime SecureCookie (may be transient across restarts, but keeps backward compatibility within a session).
	if cfg.Security.SecureCookie != nil {
		secures = append(secures, cfg.Security.SecureCookie)
	}

	return &secureCookieCipher{secures: secures}
}
