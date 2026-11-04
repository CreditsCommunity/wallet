package keys

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

var lightKDF = KDFParams{Name: "argon2id", Time: MinKDF.Time, MemoryKiB: MinKDF.MemoryKiB, Threads: 1}

func TestBackupContainer(t *testing.T) {
	plain := []byte(`{"format":"credits-wallet backup","accounts":[]}`)
	sealed, err := SealBackup(plain, []byte("backup pw"), lightKDF)
	if err != nil {
		t.Fatal(err)
	}
	if !IsBackup(sealed) || len(sealed) != BackupHeaderSize+len(plain)+16 {
		t.Fatalf("sealed: %d bytes", len(sealed))
	}
	if bytes.Contains(sealed, []byte("credits-wallet backup")) {
		t.Fatal("the content is in clear")
	}
	// the header: version, KDF, parameters
	if sealed[8] != BackupVersion || sealed[9] != 1 || binary.BigEndian.Uint32(sealed[10:]) != lightKDF.Time ||
		binary.BigEndian.Uint32(sealed[14:]) != lightKDF.MemoryKiB || sealed[18] != lightKDF.Threads {
		t.Fatalf("header % x", sealed[:BackupHeaderSize])
	}
	got, err := OpenBackup(sealed, []byte("backup pw"))
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("open: %q %v", got, err)
	}
	// a new salt and nonce each time
	again, _ := SealBackup(plain, []byte("backup pw"), lightKDF)
	if bytes.Equal(again[19:BackupHeaderSize], sealed[19:BackupHeaderSize]) {
		t.Fatal("the same salt and nonce twice")
	}

	if _, err := OpenBackup(sealed, []byte("wrong")); !errors.Is(err, ErrBackupOpen) {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := OpenBackup(sealed, nil); !errors.Is(err, ErrBackupOpen) {
		t.Errorf("empty password: %v", err)
	}
	if _, err := SealBackup(plain, nil, lightKDF); err == nil {
		t.Error("an empty backup password was accepted")
	}
	damaged := func(i int) []byte {
		d := append([]byte(nil), sealed...)
		d[i] ^= 0x01
		return d
	}
	// a damaged byte of the header (the KDF, the salt, the nonce) or of the body: the same error
	// as a wrong password
	for _, i := range []int{9, 13, 18, 20, 40, BackupHeaderSize - 1, BackupHeaderSize, len(sealed) - 1} {
		if _, err := OpenBackup(damaged(i), []byte("backup pw")); !errors.Is(err, ErrBackupOpen) {
			t.Errorf("byte %d damaged: %v", i, err)
		}
	}
	// truncated anywhere
	for _, n := range []int{9, 30, BackupHeaderSize, BackupHeaderSize + 10, len(sealed) - 1} {
		if _, err := OpenBackup(sealed[:n], []byte("backup pw")); !errors.Is(err, ErrBackupOpen) {
			t.Errorf("truncated to %d: %v", n, err)
		}
	}
	if _, err := OpenBackup(damaged(0), []byte("backup pw")); !errors.Is(err, ErrNotBackup) {
		t.Errorf("magic damaged: %v", err)
	}
	// an unknown version is refused with its number, before any derivation
	v2 := append([]byte(nil), sealed...)
	v2[8] = 2
	if _, err := OpenBackup(v2, []byte("backup pw")); err == nil || !strings.Contains(err.Error(), "version 2 is not supported") {
		t.Errorf("version 2: %v", err)
	}
	// parameters out of the key files' bounds are never derived
	huge := append([]byte(nil), sealed...)
	binary.BigEndian.PutUint32(huge[14:], 64<<20)
	if _, err := OpenBackup(huge, []byte("backup pw")); !errors.Is(err, ErrBackupOpen) {
		t.Errorf("4 TiB of memory: %v", err)
	}
	if _, err := OpenBackup(make([]byte, MaxBackupSize+1), []byte("x")); err == nil {
		t.Error("a file over the size limit was read")
	}
}
