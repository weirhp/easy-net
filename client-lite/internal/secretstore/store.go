package secretstore

import (
	"errors"
	"os"

	"github.com/zalando/go-keyring"
)

const defaultServiceName = "Easy-Net Lite"

type Store interface {
	Get(ref string) (string, error)
	Set(ref, value string) error
	Delete(ref string) error
}

type Keyring struct {
	serviceName string
}

func IsNotFound(err error) bool {
	return err != nil && (errors.Is(err, keyring.ErrNotFound) || os.IsNotExist(err))
}

func NewKeyring() *Keyring { return NewKeyringWithService(defaultServiceName) }

func NewKeyringWithService(name string) *Keyring {
	if name == "" {
		name = defaultServiceName
	}
	return &Keyring{serviceName: name}
}

func (k *Keyring) Get(ref string) (string, error) {
	return keyring.Get(k.serviceName, ref)
}

func (k *Keyring) Set(ref, value string) error {
	return keyring.Set(k.serviceName, ref, value)
}

func (k *Keyring) Delete(ref string) error {
	err := keyring.Delete(k.serviceName, ref)
	if err == keyring.ErrNotFound {
		return nil
	}
	return err
}
