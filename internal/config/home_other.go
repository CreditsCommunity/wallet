//go:build !unix

package wallet

import "os"

func ownedByMe(string) bool { return true }

func accountHome() (string, error) { return os.UserHomeDir() }
