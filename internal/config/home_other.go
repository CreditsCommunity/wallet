//go:build !unix

package config

import "os"

func ownedByMe(string) bool { return true }

func accountHome() (string, error) { return os.UserHomeDir() }
