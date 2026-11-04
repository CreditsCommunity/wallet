package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
)

// RememberNodeKey writes node_key = value into the profile's table ([nodes.NAME]) of the
// configuration file at path, keeping every other line (comments, other settings, other profiles)
// as it is: an existing node_key line of that table is replaced, otherwise the line goes right
// under the table's header; a profile the file does not have as a [nodes.NAME] table gets one at
// the end (with p's address and network); a file that does not exist is created (mode 600) with
// that profile. The new text must parse to the same configuration but for that node_key, or
// nothing is written. Written whole to a temporary file in the same folder, synced, then renamed
// over the file (a link is followed to its target, keeping the link).
func RememberNodeKey(path string, p Profile, value string) error {
	if p.Name == "" {
		return errors.New("the node was given with --node, not a profile of the configuration: add node_key to a profile by hand")
	}
	if strings.ContainsAny(value, "\x00\n\r") {
		return errors.New("not a file name")
	}
	path, err := checkConfigTarget(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var before *Config
	if exists {
		if before, err = Parse(string(data), path); err != nil {
			return err
		}
	}
	text := setNodeKeyText(string(data), p, value, exists)
	after, err := Parse(text, path)
	if err != nil {
		return fmt.Errorf("the configuration could not be updated safely (%v): add node_key = %s to [nodes.%s] by hand", err, tomlString(value), p.Name)
	}
	got, ok := after.Nodes[p.Name]
	if !ok || got.NodeKey != value {
		return fmt.Errorf("the configuration could not be updated safely: add node_key = %s to [nodes.%s] by hand", tomlString(value), p.Name)
	}
	if before != nil {
		// nothing else may change: the same settings, profile by profile
		want := *before
		want.Nodes = map[string]*Profile{}
		for n, q := range before.Nodes {
			c := *q
			want.Nodes[n] = &c
		}
		if _, had := want.Nodes[p.Name]; !had {
			c := p
			c.NodeKey = ""
			want.Nodes[p.Name] = &c
		}
		want.Nodes[p.Name].NodeKey = value
		if !reflect.DeepEqual(&want, after) {
			return fmt.Errorf("the configuration could not be updated without changing other settings: add node_key = %s to [nodes.%s] by hand", tomlString(value), p.Name)
		}
	}
	mode := os.FileMode(0o600) // a new file: the user's only
	if exists {
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
	}
	return writeWhole(path, []byte(text), mode)
}

// checkConfigTarget is where RememberNodeKey writes, and whether it may (Linux, macOS; the ACLs
// are not checked on Windows): the configuration file, a link's target and their folders must be
// the user's (or root's) and writable by nobody else, as the node's key (spec 4.4); the file a
// regular one. A file that does not exist yet: its folder is created (mode 700) and checked.
// Returns the path to write (a link's target: the link is kept).
func checkConfigTarget(path string) (string, error) {
	lst, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		st, err := os.Stat(dir)
		if err != nil {
			return "", err
		}
		return path, ownedSafe(dir, st, "the configuration's folder")
	case err != nil:
		return "", err
	}
	folders := []string{filepath.Dir(path)}
	real := path
	if lst.Mode()&os.ModeSymlink != 0 {
		if real, err = filepath.EvalSymlinks(path); err != nil {
			return "", fmt.Errorf("%s is a link that leads nowhere: %w", path, err)
		}
		folders = append(folders, filepath.Dir(real))
	}
	for _, d := range folders {
		st, err := os.Stat(d)
		if err != nil {
			return "", err
		}
		if err := ownedSafe(d, st, "the configuration's folder"); err != nil {
			return "", err
		}
	}
	st, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file: refused", real)
	}
	return real, ownedSafe(real, st, "the configuration file")
}

func writeWhole(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

var (
	headerRE  = regexp.MustCompile(`^\s*\[`)
	nodeKeyRE = regexp.MustCompile(`^(\s*)node_key\s*=`)
	bareKeyRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// tableHeader is the header of a profile's table: [nodes.NAME], with NAME bare or quoted.
func tableHeaderRE(name string) *regexp.Regexp {
	q := regexp.QuoteMeta(name)
	return regexp.MustCompile(`^\s*\[\s*nodes\s*\.\s*(` + q + `|"` + q + `"|'` + q + `')\s*\]\s*(#.*)?$`)
}

func tableName(name string) string {
	if bareKeyRE.MatchString(name) {
		return name
	}
	return tomlString(name)
}

func setNodeKeyText(text string, p Profile, value string, exists bool) string {
	line := "node_key = " + tomlString(value)
	if !exists {
		return fmt.Sprintf("# credits-wallet configuration (see 'credits-wallet config')\nnode = %s\n\n[nodes.%s]\naddress = %s\nnetwork = %s\n%s\n",
			tomlString(p.Name), tableName(p.Name), tomlString(p.Address), tomlString(string(p.Network)), line)
	}
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	header := tableHeaderRE(p.Name)
	for i, l := range lines {
		if !header.MatchString(l) {
			continue
		}
		for j := i + 1; j < len(lines) && !headerRE.MatchString(lines[j]); j++ {
			if m := nodeKeyRE.FindStringSubmatch(lines[j]); m != nil {
				lines[j] = m[1] + line
				return strings.Join(lines, nl)
			}
		}
		lines = append(lines[:i+1], append([]string{line}, lines[i+1:]...)...)
		return strings.Join(lines, nl)
	}
	if !strings.HasSuffix(text, "\n") && text != "" {
		text += nl
	}
	return text + strings.Join([]string{"", "[nodes." + tableName(p.Name) + "]", "address = " + tomlString(p.Address),
		"network = " + tomlString(string(p.Network)), line, ""}, nl)
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
