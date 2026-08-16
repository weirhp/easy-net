package launch

import "easy-net/client-lite/internal/socksprobe"

func checkSOCKS5Proxy(address string) error {
	return socksprobe.Check(address)
}
