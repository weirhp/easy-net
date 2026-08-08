package datagram

import (
	"errors"
	"testing"
)

func TestFrameRoundTripsAddresses(t *testing.T) {
	for _, address := range []string{"127.0.0.1:53", "[2001:db8::1]:5353", "dns.example:443"} {
		frame, err := Encode(address, []byte("payload"))
		if err != nil {
			t.Fatal(err)
		}
		decoded, payload, err := Decode(frame)
		if err != nil {
			t.Fatal(err)
		}
		if decoded != address || string(payload) != "payload" {
			t.Fatalf("unexpected round trip: %q %q", decoded, payload)
		}
	}
}

func TestSOCKS5FrameRejectsFragments(t *testing.T) {
	packet, err := EncodeSOCKS5("127.0.0.1:53", []byte("dns"))
	if err != nil {
		t.Fatal(err)
	}
	packet[2] = 1
	if _, _, err := DecodeSOCKS5(packet); !errors.Is(err, ErrFragmented) {
		t.Fatalf("unexpected error: %v", err)
	}
}
