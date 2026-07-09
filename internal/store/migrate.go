package store

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

// legacyStoreDir is where the pre-rewrite bridge kept its databases:
// ./whatsapp-bridge/store relative to the process working directory.
func legacyStoreDir() string {
	return filepath.Join("whatsapp-bridge", "store")
}

// MigrateLegacy copies messages.db/whatsapp.db (and -wal/-shm siblings) from
// legacyDir into storeDir when storeDir has no databases yet. It preserves
// the WhatsApp session so users don't re-scan the QR after upgrading.
func MigrateLegacy(legacyDir, storeDir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(storeDir, "whatsapp.db")); err == nil {
		return false, nil // já migrado / já em uso
	}
	if _, err := os.Stat(filepath.Join(legacyDir, "whatsapp.db")); err != nil {
		return false, nil // nada para migrar
	}
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		return false, err
	}
	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		return false, err
	}
	copied := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(legacyDir, e.Name()), filepath.Join(storeDir, e.Name())); err != nil {
			return copied, err
		}
		copied = true
	}
	if copied {
		log.Printf("migrated legacy store from %s to %s", legacyDir, storeDir)
	}
	return copied, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
