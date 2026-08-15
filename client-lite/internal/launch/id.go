package launch

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"
)

func newID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}
