package tui

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"math/rand/v2"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

type accStep int

const (
	accForm   accStep = iota
	accWords          // new: the phrase is shown
	accCheck          // new: three words typed back
	accSaving         // Argon2id takes a moment
	accSaved
)

type accountScreen struct {
	importing bool
	step      accStep
	form      *form // Name, (Secret,) Password, Repeat
	words     []string
	key       ed25519.PrivateKey
	check     *form
	positions []int
	fromFile  string
	kind      string
	address   string
	err       error
}

type savedMsg struct {
	address string
	err     error
}

func newAccountScreen(importing bool) *accountScreen {
	s := &accountScreen{importing: importing}
	if importing {
		s.form = newForm("Name", "Secret", "Password", "Repeat").secret(1).secret(2).secret(3)
		s.form.inputs[1].Placeholder = "base58 key, 24 words, keys JSON, or a file path"
	} else {
		s.form = newForm("Name", "Password", "Repeat").secret(1).secret(2)
	}
	return s
}

func (s *accountScreen) title() string {
	if s.importing {
		return "Import an account"
	}
	return "New account"
}

func (s *accountScreen) forget() {
	keys.Zero(s.key)
	s.key, s.words = nil, nil
	s.form.wipe()
	if s.check != nil {
		s.check.wipe()
	}
}

func (s *accountScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case savedMsg:
		s.address, s.err = msg.address, msg.err
		s.forget()
		s.step = accSaved
		return nil
	case tea.KeyMsg:
		if msg.String() == "esc" && s.step != accSaving {
			s.forget()
			return m.back()
		}
	}
	switch s.step {
	case accForm:
		cmd, done := s.form.update(msg)
		if !done {
			return cmd
		}
		s.err = nil
		name := s.form.value(0)
		if err := keys.ValidName(name); err != nil {
			s.err = err
			return nil
		}
		if m.w.Store.Exists(name) {
			s.err = fmt.Errorf("an account named %q already exists", name)
			return nil
		}
		n := len(s.form.inputs)
		pw, again := []byte(s.form.inputs[n-2].Value()), []byte(s.form.inputs[n-1].Value())
		defer keys.Zero(again)
		if len(pw) == 0 || !bytes.Equal(pw, again) {
			keys.Zero(pw)
			s.err = fmt.Errorf("the passwords are empty or differ")
			return nil
		}
		keys.Zero(pw)
		if s.importing {
			text := s.form.inputs[1].Value()
			if st, err := os.Stat(strings.TrimSpace(text)); err == nil && !st.IsDir() {
				data, err := os.ReadFile(strings.TrimSpace(text))
				if err != nil {
					s.err = err
					return nil
				}
				s.fromFile = strings.TrimSpace(text)
				text = string(data)
				keys.Zero(data)
			}
			key, kind, err := keys.ParseSecret(text)
			if err != nil {
				s.err = err
				return nil
			}
			s.key, s.kind = key, kind
			return s.save(m)
		}
		master, err := keys.NewMasterSeed()
		if err != nil {
			s.err = err
			return nil
		}
		s.words = keys.Words(&master)
		s.key = keys.DeriveKey(&master)
		keys.Zero(master[:])
		s.step = accWords
		return nil
	case accWords:
		if k, ok := msg.(tea.KeyMsg); ok && k.String() == "enter" {
			s.positions = rand.Perm(len(s.words))[:3]
			sort.Ints(s.positions)
			s.check = newForm(fmt.Sprintf("Word %d", s.positions[0]+1), fmt.Sprintf("Word %d", s.positions[1]+1), fmt.Sprintf("Word %d", s.positions[2]+1))
			s.step = accCheck
		}
		return nil
	case accCheck:
		cmd, done := s.check.update(msg)
		if !done {
			return cmd
		}
		for i, p := range s.positions {
			if !strings.EqualFold(s.check.value(i), s.words[p]) {
				s.err = fmt.Errorf("word %d does not match: look at the phrase again", p+1)
				s.check.wipe()
				s.check.setFocus(0)
				s.step = accWords
				return nil
			}
		}
		s.err = nil
		return s.save(m)
	case accSaved:
		if _, ok := msg.(tea.KeyMsg); ok {
			return m.back()
		}
	}
	return nil
}

// save encrypts the key in the background (Argon2id with 256 MiB takes about a second).
func (s *accountScreen) save(m *model) tea.Cmd {
	s.step = accSaving
	n := len(s.form.inputs)
	name, pw, key := s.form.value(0), []byte(s.form.inputs[n-2].Value()), s.key
	s.form.wipe()
	s.key = nil
	return func() tea.Msg {
		defer keys.Zero(pw)
		defer keys.Zero(key)
		f, err := m.w.CreateAccount(name, key, pw)
		if err != nil {
			return savedMsg{err: err}
		}
		return savedMsg{address: f.Public}
	}
}

func (s *accountScreen) view(m *model) string {
	var b strings.Builder
	switch s.step {
	case accForm:
		b.WriteString(s.form.view())
		if s.importing {
			b.WriteString("\n" + stDim.Render("The secret: a base58 key (NodePrivate.txt), the node's 24-word phrase, or the") + "\n")
			b.WriteString(stDim.Render("official wallets' keys JSON; paste it, or give the path of the file.") + "\n")
		} else {
			b.WriteString("\n" + stDim.Render("The password encrypts the key file (Argon2id + XChaCha20-Poly1305).") + "\n")
		}
		b.WriteString("\n" + keyHelp("Enter", "next", "Esc", "cancel") + "\n")
	case accWords:
		b.WriteString(stWarn.Render("Your seed phrase. Write it down on paper: it is shown only now,") + "\n")
		b.WriteString(stWarn.Render("and anyone who has it owns the account.") + "\n\n")
		for i, w := range s.words {
			fmt.Fprintf(&b, "%2d. %-10s", i+1, w)
			if i%6 == 5 {
				b.WriteString("\n")
			}
		}
		b.WriteString("\n" + keyHelp("Enter", "I wrote it down", "Esc", "cancel") + "\n")
	case accCheck:
		b.WriteString("Type back three of the words.\n\n")
		b.WriteString(s.check.view())
		b.WriteString("\n" + keyHelp("Enter", "next", "Esc", "cancel") + "\n")
	case accSaving:
		b.WriteString(stDim.Render("encrypting the key file…") + "\n")
	case accSaved:
		if s.err == nil {
			b.WriteString(stOK.Render("✓ account saved") + "\n")
			b.WriteString("address " + s.address + "\n")
			if s.kind != "" {
				b.WriteString(stDim.Render("imported from a "+s.kind) + "\n")
			}
			if s.fromFile != "" {
				b.WriteString(stWarn.Render(wallet.PlainKeyAdvice(s.fromFile)) + "\n")
			}
		}
		b.WriteString("\n" + stDim.Render("any key: back") + "\n")
	}
	if s.err != nil {
		b.WriteString("\n" + stBad.Render(oneLine(s.err.Error())) + "\n")
	}
	return stBox.Width(min(m.width-2, 78)).Render(strings.TrimRight(b.String(), "\n"))
}
