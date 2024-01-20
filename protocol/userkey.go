package protocol

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
)

type UserKey [10]byte

var ErrInvalidKey = errors.New("invalid key")

func NewUserKey() (UserKey, error) {
	var key UserKey
	_, err := rand.Read(key[:])
	if err != nil {
		return key, err
	}
	return key, nil
}

func (k UserKey) IsZero() bool {
	return k == UserKey{}
}

func (k UserKey) String() string {
	return base32.StdEncoding.EncodeToString(k[:])
}

func UserKeyFromString(keyStr string) (UserKey, error) {
	var key UserKey
	data, err := base32.StdEncoding.DecodeString(keyStr)
	if err != nil || len(data) != len(key) {
		return key, ErrInvalidKey
	}
	copy(key[:], data[:])
	return key, nil
}

func (k UserKey) MarshalJSON() ([]byte, error) {
	if k.IsZero() {
		return []byte(`""`), nil
	}
	return []byte(fmt.Sprintf(`"%s"`, k.String())), nil
}

func (k *UserKey) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || string(data) == `""` {
		*k = UserKey{}
		return nil
	}
	if len(data) < 3 || data[0] != '"' || data[len(data)-1] != '"' {
		return ErrInvalidKey
	}
	key, err := UserKeyFromString(string(data[1 : len(data)-1]))
	if err != nil {
		return err
	}
	*k = key
	return nil
}
