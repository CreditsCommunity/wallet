package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/CreditsCommunity/wallet/internal/node"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

type sendStep int

const (
	sendFields sendStep = iota
	sendPreparing
	sendReview
	sendSending
	sendDone
)

// sendScreen signs and sends one transaction of the selected account: a CS transfer (with
// optional user data), a delegation, the withdrawal of a delegation, or a token transfer. Every
// kind has the same steps: the fields, the checks (wallet.Prepare*), the review with the
// confirmation (on MAINNET: type send, then the password), the result.
type sendScreen struct {
	kind    wallet.Kind
	account *keys.File
	step    sendStep
	fields  *form
	confirm *form // (Confirm,) Password
	plan    *wallet.SendPlan
	result  wallet.SendResult
	err     error

	// the review scrolls when it is taller than the screen (a call's arguments are never cut):
	// scroll is its first line shown, seenEnd that its last line was shown, which signing needs
	scroll  int
	seenEnd bool

	// a planned screen (contracts) has no fields: planFn checks the transaction, and the screen
	// returns to parent, telling onDone the outcome
	planFn func(ctx context.Context) (*wallet.SendPlan, error)
	parent screen
	onDone func(wallet.SendResult, error)
}

// newPlannedScreen reviews, confirms and sends a transaction that planFn prepares (a deploy, a
// contract call, a payment), with the same confirmation as a transfer; it returns to parent.
func newPlannedScreen(m *model, kind wallet.Kind, f *keys.File, parent screen, planFn func(ctx context.Context) (*wallet.SendPlan, error),
	onDone func(wallet.SendResult, error)) (*sendScreen, tea.Cmd) {
	s := &sendScreen{kind: kind, account: f, step: sendPreparing, planFn: planFn, parent: parent, onDone: onDone}
	return s, s.prepare(m)
}

// leave goes back to where the screen was opened from.
func (s *sendScreen) leave(m *model) tea.Cmd {
	if s.parent != nil {
		m.screen = s.parent
		return m.refreshCurrent()
	}
	return m.back(m.refreshCurrent())
}

type preparedMsg struct {
	plan *wallet.SendPlan
	err  error
}
type sentMsg struct {
	res wallet.SendResult
	err error
}

func newSendScreen(f *keys.File) *sendScreen {
	s := &sendScreen{kind: wallet.KindTransfer, account: f, fields: newForm("To", "Amount", "Data").address(0)}
	s.fields.inputs[1].Placeholder = "CS, e.g. 12.5"
	s.fields.inputs[2].Placeholder = "optional text, public on the chain"
	s.fields.inputs[2].CharLimit = 4096
	return s
}

// newDelegateScreen delegates CS to a node (withdraw false) or withdraws from a delegation.
func newDelegateScreen(f *keys.File, withdraw bool, nodeAddr string) *sendScreen {
	s := &sendScreen{kind: wallet.KindDelegate, account: f, fields: newForm("Node", "Amount", "Until").address(0)}
	s.fields.inputs[2].Placeholder = "empty: no time limit; or 2027-01-31 / 2027-01-31 18:00 (UTC)"
	if withdraw {
		s.kind, s.fields = wallet.KindUndelegate, newForm("Node", "Amount").address(0)
	}
	s.fields.inputs[1].Placeholder = "CS, at least 5"
	if nodeAddr != "" {
		s.fields.inputs[0].SetValue(nodeAddr)
		s.fields.setFocus(1)
	}
	return s
}

func newTokenSendScreen(f *keys.File, token string) *sendScreen {
	s := &sendScreen{kind: wallet.KindToken, account: f, fields: newForm("Token", "To", "Amount").address(0, 1)}
	s.fields.inputs[0].Placeholder = "token contract address"
	s.fields.inputs[2].Placeholder = "tokens, at most the token's decimals"
	if token != "" {
		s.fields.inputs[0].SetValue(token)
		s.fields.setFocus(1)
	}
	return s
}

func (s *sendScreen) title() string {
	switch s.kind {
	case wallet.KindDelegate:
		return "Delegate CS"
	case wallet.KindUndelegate:
		return "Withdraw a delegation"
	case wallet.KindToken:
		return "Send tokens"
	case wallet.KindDeploy:
		return "Deploy a contract"
	case wallet.KindCall:
		return "Call a contract"
	case wallet.KindPay:
		return "Pay a contract"
	}
	return "Send CS"
}

func (s *sendScreen) prepare(m *model) tea.Cmd {
	if s.planFn != nil {
		planFn := s.planFn
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
			defer cancel()
			plan, err := planFn(ctx)
			return preparedMsg{plan, err}
		}
	}
	account, v := s.account.Name, s.fields.value
	kind := s.kind
	var data string
	if kind == wallet.KindTransfer {
		data = s.fields.inputs[2].Value() // as typed: spaces count
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		var plan *wallet.SendPlan
		var err error
		switch kind {
		case wallet.KindTransfer:
			plan, err = m.w.PrepareSend(ctx, wallet.SendRequest{Account: account, To: v(0), Amount: v(1), Data: data})
		case wallet.KindDelegate:
			var until time.Time
			if v(2) != "" {
				if until, err = wallet.ParseUntil(v(2)); err != nil {
					return preparedMsg{nil, err}
				}
			}
			plan, err = m.w.PrepareDelegate(ctx, account, v(0), v(1), until)
		case wallet.KindUndelegate:
			plan, err = m.w.PrepareUndelegate(ctx, account, v(0), v(1))
		case wallet.KindToken:
			plan, err = m.w.PrepareTokenSend(ctx, account, v(0), v(1), v(2))
		}
		return preparedMsg{plan, err}
	}
}

func (s *sendScreen) update(m *model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case preparedMsg:
		if s.step != sendPreparing {
			return nil
		}
		s.plan, s.err = msg.plan, msg.err
		if s.err != nil {
			s.step = sendFields
			return nil
		}
		s.step = sendReview
		if s.plan.Network.IsMainnet() {
			s.confirm = newForm("Confirm", "Password").secret(1)
			s.confirm.inputs[0].Placeholder = "type send"
		} else {
			s.confirm = newForm("Password").secret(0)
		}
		return nil
	case sentMsg:
		s.result, s.err = msg.res, msg.err
		s.step = sendDone
		if s.onDone != nil {
			s.onDone(msg.res, msg.err)
		}
		return m.refreshCurrent()
	case tea.KeyMsg:
		if msg.String() == "esc" {
			switch s.step {
			case sendSending:
				return nil // the transaction is on its way: wait for the answer
			case sendReview:
				s.confirm.wipe()
				if s.planFn != nil {
					return s.leave(m)
				}
				s.step, s.err = sendFields, nil
				return nil
			}
			return s.leave(m)
		}
	}
	switch s.step {
	case sendFields:
		if s.fields == nil {
			// a planned screen whose check failed: Enter checks again
			if k, ok := msg.(tea.KeyMsg); ok && k.String() == "enter" {
				s.step, s.err = sendPreparing, nil
				return s.prepare(m)
			}
			return nil
		}
		cmd, done := s.fields.update(msg)
		if done {
			s.step, s.err = sendPreparing, nil
			return s.prepare(m)
		}
		return cmd
	case sendReview:
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "pgdown", "ctrl+d":
				s.scroll += max(m.height-15, 1)
				return nil
			case "pgup", "ctrl+u":
				s.scroll = max(s.scroll-max(m.height-15, 1), 0)
				return nil
			}
		}
		cmd, done := s.confirm.update(msg)
		if !done {
			return cmd
		}
		if !s.seenEnd {
			s.err = fmt.Errorf("the review does not fit the screen: read it to the end with PgDn before signing")
			return nil
		}
		pwIndex := len(s.confirm.inputs) - 1
		if s.plan.Network.IsMainnet() && s.confirm.value(0) != "send" {
			s.err = fmt.Errorf("type send in Confirm to send on MAINNET")
			s.confirm.setFocus(0)
			return nil
		}
		pw := []byte(s.confirm.inputs[pwIndex].Value())
		s.confirm.wipe()
		if len(pw) == 0 {
			s.err = fmt.Errorf("the password is empty")
			return nil
		}
		// the plan is a few seconds old at most; an old one is prepared again
		if time.Since(s.plan.Prepared) > 2*time.Minute {
			keys.Zero(pw)
			s.step, s.err = sendFields, fmt.Errorf("the check is more than 2 minutes old: press Enter to check again")
			return nil
		}
		s.step, s.err = sendSending, nil
		plan := s.plan
		return func() tea.Msg {
			res, err := m.w.Send(context.Background(), plan, pw, 120*time.Second)
			keys.Zero(pw)
			return sentMsg{res, err}
		}
	case sendDone:
		if _, ok := msg.(tea.KeyMsg); ok {
			return s.leave(m)
		}
	}
	return nil
}

func untilText(t time.Time) string {
	if t.IsZero() {
		return "without a time limit"
	}
	return "until " + t.UTC().Format("2006-01-02 15:04 UTC")
}

// untilLocal is untilText with the local time, for the confirmation of a delegation.
func untilLocal(t time.Time) string {
	if t.IsZero() {
		return untilText(t)
	}
	return untilText(t) + " (" + t.Local().Format("15:04 MST") + " local)"
}

// reviewWidth is the width of a review's values (the box is 78 columns at most).
const reviewWidth = 62

// appendWrapped adds a row whose value is wrapped over as many rows as it needs, never cut.
func appendWrapped(rows [][2]string, label, value string) [][2]string {
	for i, line := range strings.Split(ansi.Hardwrap(value, reviewWidth, true), "\n") {
		if i > 0 {
			label = ""
		}
		rows = append(rows, [2]string{label, line})
	}
	return rows
}

// planLines is what the review shows of a plan, field by field.
func planLines(p *wallet.SendPlan) [][2]string {
	var rows [][2]string
	switch p.Kind {
	case wallet.KindTransfer:
		data := stDim.Render("(none)")
		if p.Data != "" {
			data = printable(p.Data) + stDim.Render("  public")
		}
		rows = append(rows, [2]string{"To", highlightAddress(keys.Address(p.To)) + " " + stOK.Render("✓")},
			[2]string{"Amount", p.Amount.Grouped() + " CS"}, [2]string{"Data", data})
	case wallet.KindDelegate:
		rows = append(rows, [2]string{"Node", highlightAddress(keys.Address(p.To))},
			[2]string{"Amount", p.Amount.Grouped() + " CS delegated " + untilLocal(p.Until)})
	case wallet.KindUndelegate:
		rows = append(rows, [2]string{"Node", highlightAddress(keys.Address(p.To))},
			[2]string{"Amount", p.Amount.Grouped() + " CS back, of " + p.Delegated.Grouped() + " CS delegated"})
	case wallet.KindToken:
		t := p.Token
		rows = append(rows, [2]string{"Token", highlightAddress(keys.Address(t.Contract))},
			[2]string{"", stDim.Render(fmt.Sprintf("calls itself %s %s, %d decimals", printable(t.Code), printable(t.Name), t.Decimals))})
		for _, warning := range t.Warnings {
			rows = append(rows, [2]string{"WARNING", stBad.Render(warning)})
		}
		rows = append(rows,
			[2]string{"To", highlightAddress(keys.Address(p.To)) + " " + stOK.Render("✓")},
			[2]string{"Amount", t.Amount + " " + printable(t.Code) + stDim.Render("  balance "+printable(t.Balance))})
	case wallet.KindDeploy:
		c := p.Contract
		what := fmt.Sprintf("%s, %d classes", printable(c.Name), len(c.Deploy.Classes))
		if c.Methods >= 0 {
			what += fmt.Sprintf(", %d methods", c.Methods)
		}
		rows = append(rows, [2]string{"Source", printable(filepath.Base(c.File))}, [2]string{"", stDim.Render(what)})
		if c.Deploy.TokenStandard != 0 {
			rows = append(rows, [2]string{"Token", fmt.Sprintf("standard %d: listed as a token", c.Deploy.TokenStandard)})
		}
		rows = append(rows, [2]string{"Address", keys.Address(p.To)}, [2]string{"", stDim.Render(fmt.Sprintf("from the account, inner id %d and the byte code", p.InnerID))})
	case wallet.KindCall:
		c := p.Contract
		rows = append(rows, [2]string{"Contract", highlightAddress(keys.Address(p.To))}, [2]string{"", stDim.Render(printable(c.Name))})
		rows = appendWrapped(rows, "Method", node.Signature(c.Method))
		// every argument in full, wrapped: they are what is signed
		for i, arg := range c.Method.Arguments {
			value := ""
			if i < len(c.Args) {
				value = printable(c.Args[i])
			}
			rows = appendWrapped(rows, fmt.Sprintf("Arg %d", i+1), printable(arg.Name+" ("+node.SimpleType(arg.Type)+")")+" = "+value)
		}
	case wallet.KindPay:
		data := stDim.Render("(none)")
		if p.Data != "" {
			data = printable(p.Data) + stDim.Render("  public, payable()'s userData")
		}
		rows = append(rows, [2]string{"Contract", highlightAddress(keys.Address(p.To))}, [2]string{"", stDim.Render(printable(p.Contract.Name) + ": its payable() runs")},
			[2]string{"Amount", p.Amount.Grouped() + " CS"}, [2]string{"Data", data})
	}
	fee := p.Fee.Fixed(6) + " CS expected"
	switch p.Kind {
	case wallet.KindToken, wallet.KindCall, wallet.KindPay:
		fee = "about " + p.Fee.Fixed(6) + " CS + execution"
	case wallet.KindDeploy:
		fee = "about " + p.Fee.Fixed(6) + " CS + the constructor's execution"
	}
	rows = append(rows, [2]string{"Fee", fee},
		[2]string{"Max fee", stWarn.Render(p.MaxFee.Amount().String() + " CS signed: the most the network may charge")})
	return rows
}

func (s *sendScreen) view(m *model) string {
	var b strings.Builder
	bal := "…"
	if x, ok := m.balances[s.account.Name]; ok {
		bal = x.CS.Grouped() + " CS"
	}
	fmt.Fprintf(&b, "%-10s %s  %s   balance %s\n", "From", s.account.Name, keys.Short(s.account.Public), bal)
	switch s.step {
	case sendFields, sendPreparing:
		if s.fields == nil {
			if s.step == sendPreparing {
				b.WriteString("\n" + stDim.Render("checking: node synced, network, compile or method, fee, balance, inner id…") + "\n")
			} else {
				b.WriteString("\n" + keyHelp("Enter", "check again", "Esc", "back") + "\n")
			}
			break
		}
		b.WriteString(s.fields.view())
		addrField := 0
		if s.kind == wallet.KindToken {
			addrField = 1
		}
		if to := s.fields.value(addrField); to != "" && !s.fields.isPasted(addrField) {
			if _, err := keys.ParseAddress(to); err == nil {
				b.WriteString(stOK.Render("           ✓ address") + "\n")
			} else {
				b.WriteString(stWarn.Render("           not an address yet") + "\n")
			}
		}
		if s.kind == wallet.KindUndelegate {
			b.WriteString(stDim.Render("Only a delegation without a time limit can be withdrawn; one with a limit ends by itself.") + "\n")
		}
		if s.step == sendPreparing {
			b.WriteString("\n" + stDim.Render("checking: node synced, fee, balances, inner id…") + "\n")
		} else {
			b.WriteString("\n" + keyHelp("Enter", "check", "Esc", "cancel") + "\n")
		}
	case sendReview, sendSending:
		p := s.plan
		rows := planLines(p)
		// what fits between the account line and the confirmation; the rest scrolls
		avail := max(m.height-13, 4)
		s.scroll = min(s.scroll, max(len(rows)-avail+1, 0))
		first, last := 0, len(rows)
		if len(rows) > avail {
			first, last = s.scroll, min(s.scroll+avail-1, len(rows))
		}
		if last == len(rows) {
			s.seenEnd = true
		}
		for _, row := range rows[first:last] {
			fmt.Fprintf(&b, "%-10s %s\n", row[0], row[1])
		}
		if len(rows) > avail {
			b.WriteString(stWarn.Render(fmt.Sprintf("lines %d-%d of %d  [PgUp/PgDn] scroll: read it all before signing", first+1, last, len(rows))) + "\n")
		}
		b.WriteString("\n")
		check := ansi.Truncate(oneLine(p.NetworkCheck.Describe()), m.width-6, "…")
		if p.Network.IsMainnet() {
			b.WriteString(stBad.Render("This sends a real "+string(p.Kind)+" on MAINNET.") + "\n")
			b.WriteString(stDim.Render(check) + "\n")
		} else {
			b.WriteString(stOK.Render("Network: "+p.Network.Label()+" (declared in the configuration)") + "\n")
			if p.NetworkCheck.Confirmed {
				b.WriteString(stOK.Render(check) + "\n")
			} else {
				b.WriteString(stWarn.Render(check) + "\n")
			}
		}
		if s.step == sendSending {
			b.WriteString("\n" + stDim.Render("signed and sent; waiting until it is in a block…") + "\n")
		} else {
			b.WriteString(s.confirm.view())
			b.WriteString("\n" + keyHelp("Enter", "sign and send", "Esc", "back") + "\n")
		}
	case sendDone:
		if s.err == nil {
			where := "in a block"
			if s.result.Block > 0 {
				where = "in block " + groupInt(s.result.Block)
			}
			b.WriteString(stOK.Render("✓ "+doneText(s.plan, s.result)) + "\n")
			fmt.Fprintf(&b, "%s, inner id %d, fee %s CS\n", where, s.result.InnerID, s.result.Fee.String())
		}
		b.WriteString("\n" + stDim.Render("any key: back") + "\n")
	}
	if s.err != nil {
		b.WriteString("\n" + stBad.Render(oneLine(s.err.Error())) + "\n")
	}
	return stBox.Width(min(m.width-2, 78)).Render(strings.TrimRight(b.String(), "\n"))
}

func doneText(p *wallet.SendPlan, r wallet.SendResult) string {
	to := keys.Short(keys.Address(p.To))
	switch p.Kind {
	case wallet.KindDelegate:
		return fmt.Sprintf("delegated %s CS to %s %s", p.Amount.Grouped(), to, untilText(p.Until))
	case wallet.KindUndelegate:
		return fmt.Sprintf("withdrew %s CS from %s", p.Amount.Grouped(), to)
	case wallet.KindToken:
		return fmt.Sprintf("sent %s %s to %s (returned %s)", p.Token.Amount, printable(p.Token.Code), to, printable(r.Returned))
	case wallet.KindDeploy:
		return fmt.Sprintf("deployed %s at %s", printable(p.Contract.Name), keys.Address(p.To))
	case wallet.KindCall:
		return fmt.Sprintf("called %s: returned %s", printable(p.Contract.Method.Name), ansi.Truncate(printable(r.Returned), 40, "…"))
	case wallet.KindPay:
		return fmt.Sprintf("paid %s CS to %s (returned %s)", p.Amount.Grouped(), to, ansi.Truncate(printable(r.Returned), 30, "…"))
	}
	return fmt.Sprintf("sent %s CS to %s", p.Amount.Grouped(), to)
}
