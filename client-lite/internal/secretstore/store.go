package secretstore

import (
	"errors"
	"os"

	"github.com/zalando/go-keyring"
)

const serviceName = "Easy-Net Lite"

type Store interface {
	Get(ref string) (string, error)
	Set(ref, value string) error
	Delete(ref string) error
}

type Keyring struct{}

func IsNotFound(err error) bool {
	return err != nil && (errors.Is(err, keyring.ErrNotFound) || os.IsNotExist(err))
}

func NewKeyring() *Keyring { return &Keyring{} }

func (k *Keyring) Get(ref string) (string, error) {
	return keyring.Get(serviceName, ref)
}

func (k *Keyring) Set(ref, value string) error {
	return keyring.Set(serviceName, ref, value)
}

func (k *Keyring) Delete(ref string) error {
	err := keyring.Delete(serviceName, ref)
	if err == keyring.ErrNotFound {
		return nil
	}
	return err
}
