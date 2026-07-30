package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

func TestHostKeyRequiresFirstUseConfirmation(t *testing.T) {
	publicKey := testPublicKey(t)
	transport := New(Config{Address: "ssh.example.com:22", Username: "user", Password: "password"})
	clientConfig, err := transport.clientConfig()
	if err != nil {
		t.Fatal(err)
	}
	err = clientConfig.HostKeyCallback("ssh.example.com:22", &net.TCPAddr{}, publicKey)
	var unknown *HostKeyUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected HostKeyUnknownError, got %v", err)
	}
	if unknown.Fingerprint != gossh.FingerprintSHA256(publicKey) {
		t.Fatalf("unexpected fingerprint %q", unknown.Fingerprint)
	}
}

func TestHostKeyAcceptsOnlySavedFingerprint(t *testing.T) {
	publicKey := testPublicKey(t)
	fingerprint := gossh.FingerprintSHA256(publicKey)
	transport := New(Config{Address: "ssh.example.com:22", Username: "user", Password: "password", HostKeyFingerprint: fingerprint})
	clientConfig, err := transport.clientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := clientConfig.HostKeyCallback("ssh.example.com:22", &net.TCPAddr{}, publicKey); err != nil {
		t.Fatal(err)
	}

	transport.cfg.HostKeyFingerprint = "SHA256:different"
	clientConfig, err = transport.clientConfig()
	if err != nil {
		t.Fatal(err)
	}
	err = clientConfig.HostKeyCallback("ssh.example.com:22", &net.TCPAddr{}, publicKey)
	var mismatch *HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected HostKeyMismatchError, got %v", err)
	}
}

func testPublicKey(t *testing.T) gossh.PublicKey {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := gossh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
