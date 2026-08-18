package panel

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const passwordIterations = 120000

func newUser(username, password, role string) (User, error) {
	username = strings.TrimSpace(username)
	if !validUsername(username) {
		return User{}, fmt.Errorf("用户名只能包含字母、数字、点、横线和下划线，长度 3–32 位")
	}
	if len(password) < 6 || len(password) > 128 {
		return User{}, fmt.Errorf("密码长度必须为 6–128 位")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return User{}, err
	}
	return User{Username: username, Role: role, PasswordSalt: hex.EncodeToString(salt), PasswordHash: derivePassword(password, salt), ProxyPassword: password, CreatedAt: time.Now().UTC()}, nil
}

func validUsername(value string) bool {
	if len(value) < 3 || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func derivePassword(password string, salt []byte) string {
	value := append(append([]byte(nil), salt...), []byte(password)...)
	sum := sha256.Sum256(value)
	block := sum[:]
	for i := 1; i < passwordIterations; i++ {
		h := sha256.New()
		h.Write(block)
		h.Write(salt)
		h.Write([]byte(password))
		block = h.Sum(nil)
	}
	return hex.EncodeToString(block)
}

func verifyPassword(user User, password string) bool {
	salt, err := hex.DecodeString(user.PasswordSalt)
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(user.PasswordHash)
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(derivePassword(password, salt))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

func randomSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func signSession(secret, username string, expires time.Time) string {
	payload := username + "|" + strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func parseSession(secret, token string, now time.Time) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", false
	}
	fields := strings.Split(string(payload), "|")
	if len(fields) != 2 {
		return "", false
	}
	expires, err := strconv.ParseInt(fields[1], 10, 64)
	return fields[0], err == nil && now.Unix() < expires
}
