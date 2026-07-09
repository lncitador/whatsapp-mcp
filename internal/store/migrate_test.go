package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyCopiesOnce(t *testing.T) {
	legacy := t.TempDir()
	dest := t.TempDir()
	os.WriteFile(filepath.Join(legacy, "messages.db"), []byte("legacy-messages"), 0644)
	os.WriteFile(filepath.Join(legacy, "whatsapp.db"), []byte("legacy-session"), 0644)

	migrated, err := MigrateLegacy(legacy, dest)
	if err != nil || !migrated {
		t.Fatalf("migrated=%v err=%v", migrated, err)
	}
	got, _ := os.ReadFile(filepath.Join(dest, "whatsapp.db"))
	if string(got) != "legacy-session" {
		t.Fatalf("whatsapp.db not copied")
	}

	// segunda chamada: destino não-vazio -> no-op
	os.WriteFile(filepath.Join(legacy, "messages.db"), []byte("changed"), 0644)
	migrated, err = MigrateLegacy(legacy, dest)
	if err != nil || migrated {
		t.Fatalf("second run migrated=%v err=%v, want false,nil", migrated, err)
	}
}

func TestMigrateLegacyNoSource(t *testing.T) {
	migrated, err := MigrateLegacy(filepath.Join(t.TempDir(), "nope"), t.TempDir())
	if err != nil || migrated {
		t.Fatalf("migrated=%v err=%v, want false,nil", migrated, err)
	}
}
