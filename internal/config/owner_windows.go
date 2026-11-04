package config

import "os"

// ownedSafe: the ACLs are not checked on Windows (the wallet sets none itself).
func ownedSafe(path string, st os.FileInfo, what string) error { return nil }
