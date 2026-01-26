package services

import (
	"github.com/gorilla/securecookie"
	"github.com/muety/wakapi/config"
)

// TokenCipher provides basic symmetric encryption for secrets at rest.
type TokenCipher interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}

type secureCookieCipher struct {
	secure *securecookie.SecureCookie
}

func (c *secureCookieCipher) Encrypt(value string) (string, error) {
	encoded, err := c.secure.Encode("token", value)
	if err != nil {
		return "", err
	}
	return encoded, nil
}

func (c *secureCookieCipher) Decrypt(value string) (string, error) {
	var decoded string
	if err := c.secure.Decode("token", value, &decoded); err != nil {
		return "", err
	}
	return decoded, nil
}

func newTokenCipher() TokenCipher {
	return &secureCookieCipher{secure: config.Get().Security.SecureCookie}
}
