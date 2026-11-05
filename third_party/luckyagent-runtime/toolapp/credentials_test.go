package tool

import (
	"context"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/credentials"
)

func TestCredentialProviderOpensHostStoreForEachCall(t *testing.T) {
	home := t.TempDir()
	store, err := credentials.NewStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "test-key", "token", "default", "fixture-secret"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	provider := credentialProvider{homeDir: home}
	for range 2 {
		err := provider.WithCredential(context.Background(), "test-key", func(secret []byte) error {
			if string(secret) != "fixture-secret" {
				t.Fatal("credential changed across adapter")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := provider.WithCredential(context.Background(), "missing-key", func([]byte) error { t.Fatal("missing credential reached callback"); return nil }); err == nil {
		t.Fatal("missing credential accepted")
	}
}
