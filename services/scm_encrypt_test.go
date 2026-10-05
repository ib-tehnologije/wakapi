package services

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/securecookie"
	"github.com/muety/wakapi/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tokenCipherTestSalt  = "scm-encrypt-test-salt"
	tokenCipherTestValue = "not-a-real-token"
	// securecookie.New() defaults to a max age of 30 days
	secureCookieDefaultMaxAge = 30 * 24 * time.Hour
)

func tokenCipherTestKeys() (hashKey, blockKey []byte) {
	hash := sha512.Sum512([]byte(tokenCipherTestSalt))
	return hash[:32], hash[32:]
}

func setupTokenCipherTestConfig(t *testing.T) {
	t.Helper()
	prev := config.Get()
	t.Cleanup(func() { config.Set(prev) })

	cfg := config.Empty()
	cfg.Security.PasswordSalt = tokenCipherTestSalt
	config.Set(cfg)
}

// backdateToken re-stamps a securecookie-encoded value, so that it is indistinguishable from one
// that was encoded `age` ago with the same keys. The wire format of gorilla/securecookie is
// base64url(timestamp "|" base64url(ciphertext) "|" hmac-sha256(name "|" timestamp "|" base64url(ciphertext))).
func backdateToken(t *testing.T, encoded string, hashKey []byte, age time.Duration) string {
	t.Helper()

	raw, err := base64.URLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	parts := bytes.SplitN(raw, []byte("|"), 3)
	require.Len(t, parts, 3)

	timestamp := strconv.FormatInt(time.Now().Add(-age).UTC().Unix(), 10)
	payload := append([]byte(timestamp+"|"), parts[1]...)

	mac := hmac.New(sha256.New, hashKey)
	mac.Write(append([]byte("token|"), payload...))

	return base64.URLEncoding.EncodeToString(append(append(payload, '|'), mac.Sum(nil)...))
}

func TestTokenCipher_RoundTrip(t *testing.T) {
	setupTokenCipherTestConfig(t)

	encrypted, err := newTokenCipher().Encrypt(tokenCipherTestValue)
	require.NoError(t, err)
	assert.NotContains(t, encrypted, tokenCipherTestValue)

	// a new cipher instance (e.g. after a restart) must be able to read it
	decrypted, err := newTokenCipher().Decrypt(encrypted)
	require.NoError(t, err)
	assert.Equal(t, tokenCipherTestValue, decrypted)
}

func TestTokenCipher_DecryptsTokenOlderThanDefaultMaxAge(t *testing.T) {
	setupTokenCipherTestConfig(t)
	hashKey, blockKey := tokenCipherTestKeys()

	sut := newTokenCipher()
	encrypted, err := sut.Encrypt(tokenCipherTestValue)
	require.NoError(t, err)

	// a codec set up like the token cipher's, but with securecookie's defaults (i.e. 30 days max age)
	defaultCodec := securecookie.New(hashKey, blockKey)

	// sanity check: a re-stamped, but not aged value is valid for both, so failures below are caused by age only
	var decoded string
	fresh := backdateToken(t, encrypted, hashKey, 0)
	require.NoError(t, defaultCodec.Decode("token", fresh, &decoded))
	assert.Equal(t, tokenCipherTestValue, decoded)
	decrypted, err := sut.Decrypt(fresh)
	require.NoError(t, err)
	assert.Equal(t, tokenCipherTestValue, decrypted)

	for _, age := range []time.Duration{
		secureCookieDefaultMaxAge + 24*time.Hour, // one day past the default max age
		400 * 24 * time.Hour,                     // more than a year
	} {
		aged := backdateToken(t, encrypted, hashKey, age)

		// default-configured codec rejects the value as expired ...
		decoded = ""
		err := defaultCodec.Decode("token", aged, &decoded)
		require.Error(t, err, "age %s", age)
		var cookieErr securecookie.Error
		require.ErrorAs(t, err, &cookieErr, "age %s", age)
		assert.True(t, cookieErr.IsDecode(), "age %s", age)
		assert.Contains(t, err.Error(), "expired timestamp", "age %s", age)
		assert.Empty(t, decoded, "age %s", age)

		// ... while the token cipher must still be able to read it
		decrypted, err := sut.Decrypt(aged)
		require.NoError(t, err, "age %s", age)
		assert.Equal(t, tokenCipherTestValue, decrypted, "age %s", age)
	}
}

func TestTokenCipher_RejectsTamperedToken(t *testing.T) {
	setupTokenCipherTestConfig(t)

	sut := newTokenCipher()
	encrypted, err := sut.Encrypt(tokenCipherTestValue)
	require.NoError(t, err)

	// re-stamped using a wrong key, i.e. with an invalid signature
	wrongKey := bytes.Repeat([]byte{0x42}, 32)
	forged := backdateToken(t, encrypted, wrongKey, 0)

	_, err = sut.Decrypt(forged)
	assert.Error(t, err)
}
