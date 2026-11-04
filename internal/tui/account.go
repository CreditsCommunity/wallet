package tui

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/CreditsCommunity/wallet/internal/config"
	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

type accStep int

const (
	accForm      accStep = iota
	accWords             // new: the phrase is shown
	accCheck             // new: three words typed back
	accFileAsk           // new: save the words to a file as well? (default no)
	accFilePath          // new: where (default ~/credits-recovery-NAME.txt)
	accKeyFilePw         // import of an encrypted key file: its own password
	accSaving            // Argon2id takes a moment
	accSaved
)

// pwMode: the password fields of the account form.
type pwMode int

const (
	pwChoose pwMode = iota // the first account: the wallet password is chosen (Password, Repeat)
	pwOpen                 // the wallet is closed: its password (Wallet pw)
	pwNone                 // the wallet is open: no password
)

type accountScreen struct {
	importing bool
	mode      pwMode
	keyFile   *keys.File // an encrypted key file being imported
	kfForm    *form
	nodeDir   string // the node's key is made, for the node in this folder ("": an account of the store)
	step      accStep
	form      *form // Name, (Secret,) Password, Repeat
	words     []string
	key       ed25519.PrivateKey
	check     *form
	file      *form  // the recovery file's path
	recovery  string // the recovery file to write, then written
	positions []int
	fromFile  string
	kind      string
	address   string
	err       error
	note      string // after saving: "address copied"
	savedName string // the account being saved (the form is wiped before)
}

type savedMsg struct {
	tried    bool // the wallet password was checked (a new account while the wallet is closed)
	address  string
	recovery string // the recovery file written, if any
	err      error
}

// newAccountScreen is the form of an empty wallet: the wallet password is chosen with it.
func newAccountScreen(importing bool) *accountScreen { return newAccountForm(importing, pwChoose) }

// newAccountScreen asks the wallet password as the wallet needs it: chosen (the first account),
// typed (the wallet is closed), or not at all (the wallet is open).
func (m *model) newAccountScreen(importing bool) *accountScreen {
	switch {
	case m.session != nil:
		return newAccountForm(importing, pwNone)
	case m.w.HasWalletPassword():
		return newAccountForm(importing, pwOpen)
	}
	return newAccountForm(importing, pwChoose)
}

func newAccountForm(importing bool, mode pwMode) *accountScreen {
	s := &accountScreen{importing: importing, mode: mode}
	labels := []string{"Name"}
	if importing {
		labels = append(labels, "Secret")
	}
	switch mode {
	case pwChoose:
		labels = append(labels, "Password", "Repeat")
	case pwOpen:
		labels = append(labels, "Wallet pw")
	}
	s.form = newForm(labels...)
	for i := 1; i < len(labels); i++ {
		s.form.secret(i)
	}
	if importing {
		s.form.inputs[1].Placeholder = "base58 key, 24 words, keys JSON, or a file path"
	}
	return s
}

// password is the wallet password typed in the form (nil: none asked).
func (s *accountScreen) password() []byte {
	switch s.mode {
	case pwChoose:
		return []byte(s.form.inputs[len(s.form.inputs)-2].Value())
	case pwOpen:
		return []byte(s.form.inputs[len(s.form.inputs)-1].Value())
	}
	return nil
}

// newNodeKeyScreen makes the node's key from a new 24-word phrase, written in the node's folder.
func newNodeKeyScreen(dir string) *accountScreen {
	return &accountScreen{nodeDir: dir, form: newForm("Confirm")}
}

// name is the account's name: the form's, or "node" for the node's key.
func (s *accountScreen) name() string {
	if s.nodeDir != "" {
		return wallet.NodeAccount
	}
	return s.form.value(0)
}

func (s *accountScreen) title() string {
	if s.nodeDir != "" {
		return "The node's key"
	}
	if s.importing {
		return "Import an account"
	}
	return "New account"
}

func (s *accountScreen) forget() {
	keys.Zero(s.key)
	clear(s.words)
	s.key, s.words = nil, nil
	s.form.wipe()
	if s.check != nil {
		s.check.wipe()
	}
	if s.kfForm != nil {
		s.kfForm.wipe()
	}
	if s.file != nil {
		s.file.wipe()
	}
}

func (s *accountScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case savedMsg:
		if msg.tried {
			m.pwTried(msg.err)
			msg.err = wrongPassword(msg.err)
		}
		name := s.savedName
		s.address, s.recovery, s.err = msg.address, msg.recovery, msg.err
		s.forget()
		s.step = accSaved
		if msg.err == nil {
			// a new account: the session locks, and the unlock screen comes next, on that account
			if s.nodeDir != "" {
				name = m.w.NodeAccountName()
			}
			if m.session != nil {
				m.lockAccount("", "") // the wallet stays open: Enter opens the new account
			} else {
				m.lockSession("", "")
			}
			m.selectName = name
			m.screen = s
		}
		return nil
	case tea.KeyMsg:
		if msg.String() == "esc" && s.step != accSaving {
			s.forget()
			return m.back()
		}
	}
	switch s.step {
	case accForm:
		if s.nodeDir != "" {
			if k, ok := msg.(tea.KeyMsg); !ok || k.String() != "enter" {
				return nil
			}
			real, err := wallet.CheckNodeKeyTarget(s.nodeDir, false)
			if err != nil {
				s.err = err
				return nil
			}
			s.nodeDir, s.err = real, nil
			return s.draw()
		}
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
		if m.w.NameTaken(name) {
			s.err = fmt.Errorf("an account named %q already exists", name)
			return nil
		}
		if m.w.NeedsMigration() {
			s.err = wallet.ErrMigrateFirst
			return nil
		}
		n := len(s.form.inputs)
		switch s.mode {
		case pwChoose:
			pw, again := []byte(s.form.inputs[n-2].Value()), []byte(s.form.inputs[n-1].Value())
			ok := len(pw) > 0 && bytes.Equal(pw, again)
			keys.Zero(pw)
			keys.Zero(again)
			if !ok {
				s.err = fmt.Errorf("the passwords are empty or differ")
				return nil
			}
		case pwOpen:
			if s.form.inputs[n-1].Value() == "" {
				s.err = fmt.Errorf("type the wallet password")
				return nil
			}
			if err := m.pwThrottle(); err != nil {
				s.err = err
				return nil
			}
		}
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
			if kf, ok := keys.EncryptedKeyFile(text); ok {
				if kf.UnderWalletKey() {
					s.err = fmt.Errorf("this account file is under another wallet's password: export it from that wallet (account export), or import its 24 words")
					return nil
				}
				s.keyFile, s.kfForm, s.step = kf, newForm("Key file pw").secret(0), accKeyFilePw
				return nil
			}
			key, kind, err := keys.ParseSecret(text)
			if err != nil {
				s.err = err
				return nil
			}
			s.key, s.kind = key, kind
			return s.save(m)
		}
		return s.draw()
	case accKeyFilePw:
		cmd, done := s.kfForm.update(msg)
		if !done {
			return cmd
		}
		pw := []byte(s.kfForm.inputs[0].Value())
		s.kfForm.wipe()
		key, err := s.keyFile.Decrypt(pw)
		keys.Zero(pw)
		if err != nil {
			s.err = err
			return nil
		}
		s.err, s.key, s.kind = nil, key, "encrypted key file"
		return s.save(m)
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
		s.check.wipe()
		s.step = accFileAsk
		return nil
	case accFileAsk:
		k, ok := msg.(tea.KeyMsg)
		if !ok {
			return nil
		}
		switch strings.ToLower(k.String()) {
		case "y":
			def, err := wallet.DefaultRecoveryPath(s.name())
			if err != nil {
				s.err = err
			}
			s.file = newForm("File")
			s.file.inputs[0].CharLimit = 4096
			s.file.inputs[0].SetValue(def)
			s.file.inputs[0].CursorEnd()
			s.step = accFilePath
			return nil
		case "n", "enter":
			return s.save(m)
		}
		return nil
	case accFilePath:
		cmd, done := s.file.update(msg)
		if !done {
			return cmd
		}
		path, err := m.w.CheckRecoveryPath(s.file.value(0), s.nodeDir)
		if err != nil {
			s.err = err
			return nil
		}
		s.err = nil
		s.recovery = path
		return s.save(m)
	case accSaved:
		if k, ok := msg.(tea.KeyMsg); ok {
			if k.String() == "y" && s.err == nil && s.address != "" {
				s.note = copyOwnAddress(s.address)
				return nil
			}
			return m.back()
		}
	}
	return nil
}

// draw makes a new master seed: its words are shown next.
func (s *accountScreen) draw() tea.Cmd {
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
}

// save encrypts the key in the background (Argon2id with 256 MiB takes about a second), or writes
// the node's key in the node's folder.
func (s *accountScreen) save(m *model) tea.Cmd {
	s.step = accSaving
	if s.nodeDir != "" {
		return s.saveNodeKey()
	}
	session := m.session
	tried := session == nil && s.mode == pwOpen
	if err := m.pwThrottle(); tried && err != nil {
		s.step, s.err = accForm, err // the words are drawn again: nothing was saved
		keys.Zero(s.key)
		clear(s.words)
		s.key, s.words = nil, nil
		return nil
	}
	name, pw, key := s.form.value(0), s.password(), s.key
	s.savedName = name
	recovery, words := s.recovery, s.words
	s.form.wipe()
	s.key, s.words, s.recovery = nil, nil, ""
	return func() tea.Msg {
		defer keys.Zero(pw)
		defer keys.Zero(key)
		defer clear(words)
		if recovery != "" {
			data := wallet.RecoveryText(name, keys.Address(key.Public().(ed25519.PublicKey)), time.Now(), words)
			err := wallet.WriteSecretFile(recovery, data)
			keys.Zero(data)
			if err != nil {
				return savedMsg{err: fmt.Errorf("the recovery file: %w; nothing was created", err)}
			}
		}
		var f *keys.File
		var err error
		if session != nil {
			f, err = m.w.CreateAccountIn(session, name, key)
		} else {
			f, err = m.w.CreateAccount(name, key, pw)
		}
		if err != nil {
			if recovery != "" {
				os.Remove(recovery)
			}
			return savedMsg{tried: tried, err: err}
		}
		return savedMsg{tried: tried, address: f.Public, recovery: recovery}
	}
}

// saveNodeKey writes the node's key files (and the recovery file first, if asked).
func (s *accountScreen) saveNodeKey() tea.Cmd {
	dir, key, recovery, words := s.nodeDir, s.key, s.recovery, s.words
	s.key, s.words, s.recovery = nil, nil, ""
	return func() tea.Msg {
		defer keys.Zero(key)
		defer clear(words)
		address := keys.Address(key.Public().(ed25519.PublicKey))
		if recovery != "" {
			data := wallet.RecoveryText(wallet.NodeAccount, address, time.Now(), words)
			err := wallet.WriteSecretFile(recovery, data)
			keys.Zero(data)
			if err != nil {
				return savedMsg{err: fmt.Errorf("the recovery file: %w; nothing was written", err)}
			}
		}
		if err := wallet.WriteNodeKey(dir, key); err != nil {
			if recovery != "" {
				os.Remove(recovery)
			}
			return savedMsg{err: fmt.Errorf("%w; nothing was written", err)}
		}
		return savedMsg{address: address, recovery: recovery}
	}
}

func (s *accountScreen) view(m *model) string {
	var b strings.Builder
	switch s.step {
	case accForm:
		if s.nodeDir != "" {
			b.WriteString("The node in " + echo.Terminal(s.nodeDir) + " has no key yet.\n\n")
			b.WriteString("The wallet makes it from a new 24-word phrase, your paper backup of the node's identity,\n")
			b.WriteString("and writes NodePrivate.txt and NodePublic.txt there (mode 600) in the node's format:\n")
			b.WriteString("the node uses them at its first start; the key is this wallet's account \"node\".\n\n")
			b.WriteString(stWarn.Width(74).Render(wallet.NodeKeyWarning) + "\n")
			b.WriteString("\n" + keyHelp("Enter", "make the key", "Esc", "cancel") + "\n")
			break
		}
		b.WriteString(s.form.view())
		if s.mode == pwChoose {
			if pw := s.form.inputs[len(s.form.inputs)-2].Value(); pw != "" {
				if hint := wallet.PasswordHint([]byte(pw)); hint != "" {
					b.WriteString(stWarn.Render(hint) + "\n")
				}
			}
			b.WriteString(stDim.Render("This is the wallet password: it encrypts every account, one password opens them all.") + "\n")
		}
		if s.importing {
			b.WriteString("\n" + stDim.Render("The secret: a base58 key (NodePrivate.txt), the node's 24-word phrase, or the") + "\n")
			b.WriteString(stDim.Render("official wallets' keys JSON; paste it, or give the path of the file.") + "\n")
		} else if s.mode != pwChoose {
			b.WriteString("\n" + stDim.Render("The account is encrypted under the wallet password.") + "\n")
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
	case accKeyFilePw:
		b.WriteString("An encrypted key file: its own password opens it, then it goes under the wallet password.\n\n")
		b.WriteString(s.kfForm.view())
		b.WriteString("\n" + keyHelp("Enter", "open", "Esc", "cancel") + "\n")
	case accCheck:
		b.WriteString("Type back three of the words.\n\n")
		b.WriteString(s.check.view())
		b.WriteString("\n" + keyHelp("Enter", "next", "Esc", "cancel") + "\n")
	case accFileAsk:
		b.WriteString(wallet.RecoveryQuestion + "\n\n")
		b.WriteString(stDim.Render("The file goes outside the wallet's folder, never next to the keys. Default: no,") + "\n")
		b.WriteString(stDim.Render("the words are only on your paper.") + "\n")
		b.WriteString("\n" + keyHelp("y", "save to a file", "n/Enter", "no", "Esc", "cancel") + "\n")
	case accFilePath:
		if s.nodeDir != "" {
			b.WriteString("Where to save the 24 words (a new file, outside the wallet's and the node's folders).\n")
		} else {
			b.WriteString("Where to save the 24 words (a new file, outside the wallet's folder).\n")
		}
		if w := config.HomeWarning(); w != "" {
			b.WriteString(stWarn.Render(w) + "\n")
		}
		b.WriteString("\n")
		b.WriteString(s.file.view())
		b.WriteString("\n" + keyHelp("Enter", "save", "Esc", "cancel") + "\n")
	case accSaving:
		if s.nodeDir != "" {
			b.WriteString(stDim.Render("writing the node's key…") + "\n")
			break
		}
		b.WriteString(stDim.Render("encrypting the key file…") + "\n")
	case accSaved:
		if s.err == nil && s.nodeDir != "" {
			b.WriteString(stOK.Render("✓ the node's key is written") + "\n")
			b.WriteString(echo.Terminal(filepath.Join(s.nodeDir, keys.NodePrivateFile)) + " and " + keys.NodePublicFile + " (mode 600)\n")
			b.WriteString("address " + s.address + "  (the node's key account)\n")
			b.WriteString(stDim.Render("The node uses this key at its next start.") + "\n")
		} else if s.err == nil {
			b.WriteString(stOK.Render("✓ account saved") + "\n")
			b.WriteString("address " + s.address + "\n")
		}
		if s.err == nil {
			if s.note != "" {
				b.WriteString(stWarn.Render(s.note) + "\n")
			}
			if s.recovery != "" {
				b.WriteString("The 24 words are in this file (mode 600):\n" + echo.Terminal(s.recovery) + "\n")
				b.WriteString(stWarn.Render(wallet.RecoveryAdvice) + "\n")
			}
			if s.kind != "" {
				b.WriteString(stDim.Render("imported from a "+s.kind) + "\n")
			}
			if s.fromFile != "" {
				b.WriteString(stWarn.Render(wallet.PlainKeyAdvice(s.fromFile)) + "\n")
			}
		}
		if s.err == nil {
			b.WriteString("\n" + keyHelp("y", "copy the address") + "  " + stDim.Render("any other key: the unlock screen") + "\n")
		} else {
			b.WriteString("\n" + stDim.Render("any key: back") + "\n")
		}
	}
	if s.err != nil {
		b.WriteString("\n" + stBad.Render(oneLine(s.err.Error())) + "\n")
	}
	return stBox.Width(min(m.width-2, 78)).Render(strings.TrimRight(b.String(), "\n"))
}
