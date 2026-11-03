package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	s := &sendScreen{kind: wallet.KindTransfer, account: f, fields: newForm("To", "Amount", "Data")}
	s.fields.inputs[1].Placeholder = "CS, e.g. 12.5"
	s.fields.inputs[2].Placeholder = "optional text, public on the chain"
	s.fields.inputs[2].CharLimit = 4096
	return s
}

// newDelegateScreen delegates CS to a node (withdraw false) or withdraws from a delegation.
func newDelegateScreen(f *keys.File, withdraw bool, nodeAddr string) *sendScreen {
	s := &sendScreen{kind: wallet.KindDelegate, account: f, fields: newForm("Node", "Amount", "Until")}
	s.fields.inputs[2].Placeholder = "empty: no time limit; or 2027-01-31 / 2027-01-31 18:00 (UTC)"
	if withdraw {
		s.kind, s.fields = wallet.KindUndelegate, newForm("Node", "Amount")
	}
	s.fields.inputs[1].Placeholder = "CS, at least 5"
	if nodeAddr != "" {
		s.fields.inputs[0].SetValue(nodeAddr)
		s.fields.setFocus(1)
	}
	return s
}

func newTokenSendScreen(f *keys.File, token string) *sendScreen {
	s := &sendScreen{kind: wallet.KindToken, account: f, fields: newForm("Token", "To", "Amount")}
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
	}
	return "Send CS"
}

func (s *sendScreen) prepare(m *model) tea.Cmd {
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
		return m.refreshCurrent()
	case tea.KeyMsg:
		if msg.String() == "esc" {
			switch s.step {
			case sendSending:
				return nil // the transaction is on its way: wait for the answer
			case sendReview:
				s.confirm.wipe()
				s.step, s.err = sendFields, nil
				return nil
			}
			return m.back(m.refreshCurrent())
		}
	}
	switch s.step {
	case sendFields:
		cmd, done := s.fields.update(msg)
		if done {
			s.step, s.err = sendPreparing, nil
			return s.prepare(m)
		}
		return cmd
	case sendReview:
		cmd, done := s.confirm.update(msg)
		if !done {
			return cmd
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
			return m.back(m.refreshCurrent())
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

// planLines is what the review shows of a plan, field by field.
func planLines(p *wallet.SendPlan) [][2]string {
	var rows [][2]string
	switch p.Kind {
	case wallet.KindTransfer:
		data := stDim.Render("(none)")
		if p.Data != "" {
			data = printable(p.Data) + stDim.Render("  public")
		}
		rows = append(rows, [2]string{"To", keys.Address(p.To) + " " + stOK.Render("✓")},
			[2]string{"Amount", p.Amount.Grouped() + " CS"}, [2]string{"Data", data})
	case wallet.KindDelegate:
		rows = append(rows, [2]string{"Node", keys.Address(p.To)},
			[2]string{"Amount", p.Amount.Grouped() + " CS delegated " + untilLocal(p.Until)})
	case wallet.KindUndelegate:
		rows = append(rows, [2]string{"Node", keys.Address(p.To)},
			[2]string{"Amount", p.Amount.Grouped() + " CS back, of " + p.Delegated.Grouped() + " CS delegated"})
	case wallet.KindToken:
		t := p.Token
		rows = append(rows, [2]string{"Token", keys.Address(t.Contract)},
			[2]string{"", stDim.Render(fmt.Sprintf("calls itself %s %s, %d decimals", printable(t.Code), printable(t.Name), t.Decimals))})
		for _, warning := range t.Warnings {
			rows = append(rows, [2]string{"WARNING", stBad.Render(warning)})
		}
		rows = append(rows,
			[2]string{"To", keys.Address(p.To) + " " + stOK.Render("✓")},
			[2]string{"Amount", t.Amount + " " + printable(t.Code) + stDim.Render("  balance "+printable(t.Balance))})
	}
	fee := p.Fee.Fixed(6) + " CS expected"
	if p.Kind == wallet.KindToken {
		fee = "about " + p.Fee.Fixed(6) + " CS + execution"
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
		b.WriteString(s.fields.view())
		addrField := 0
		if s.kind == wallet.KindToken {
			addrField = 1
		}
		if to := s.fields.value(addrField); to != "" {
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
		for _, row := range planLines(p) {
			fmt.Fprintf(&b, "%-10s %s\n", row[0], row[1])
		}
		b.WriteString("\n")
		if p.Network.IsMainnet() {
			b.WriteString(stBad.Render("This sends a real "+string(p.Kind)+" on MAINNET.") + "\n")
		} else {
			b.WriteString(stOK.Render("Network: "+p.Network.Label()+" (declared in the configuration)") + "\n")
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
	}
	return fmt.Sprintf("sent %s CS to %s", p.Amount.Grouped(), to)
}
