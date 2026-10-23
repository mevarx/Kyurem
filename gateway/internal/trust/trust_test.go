package trust

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2acrypto"
	"github.com/kyurem-project/kyurem/gateway/internal/config"
)

func TestEnforceBlocksUnsignedCardsAndWarnDoesNot(t *testing.T) {
	card := &a2a.AgentCard{}
	for _, tc := range []struct {
		mode    string
		blocked bool
	}{
		{mode: "enforce", blocked: true},
		{mode: "warn", blocked: false},
	} {
		v, err := New(config.Trust{Mode: tc.mode})
		if err != nil {
			t.Fatal(err)
		}
		result := v.Verify(context.Background(), "echo", []byte(`{}`), card)
		if result.Blocked() != tc.blocked || result.Status() != StatusUnsigned {
			t.Errorf("mode=%s result=%+v", tc.mode, result)
		}
	}
}

func TestEnforceAcceptsSignatureFromConfiguredKey(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKey})
	raw := json.RawMessage(`{"name":"echo","version":"1.0"}`)
	signer, err := a2acrypto.NewSigner(a2acrypto.SignerConfig{PrivateKey: privateKey, KeyID: "trusted"})
	if err != nil {
		t.Fatal(err)
	}
	signature, err := signer.Sign(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := New(config.Trust{
		Mode: "enforce",
		Keys: []config.TrustedKey{{ID: "trusted", PublicKeyPEM: string(publicPEM)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := verifier.Verify(context.Background(), "echo", raw, &a2a.AgentCard{Signatures: []a2a.AgentCardSignature{*signature}})
	if !result.Verified || result.Blocked() || result.KeyID != "trusted" {
		t.Fatalf("configured key did not verify card: %+v", result)
	}
}
