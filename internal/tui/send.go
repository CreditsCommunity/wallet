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

type sendScreen struct {
	account *keys.File
	step    sendStep
	fields  *form // To, Amount
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
	s := &sendScreen{account: f, fields: newForm("To", "Amount")}
	s.fields.inputs[1].Placeholder = "CS, e.g. 12.5"
	return s
}

func (s *sendScreen) title() string { return "Send CS" }

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
			account, to, amt := s.account.Name, s.fields.value(0), s.fields.value(1)
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
				defer cancel()
				plan, err := m.w.PrepareSend(ctx, account, to, amt)
				return preparedMsg{plan, err}
			}
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
		if to := s.fields.value(0); to != "" {
			if _, err := keys.ParseAddress(to); err == nil {
				b.WriteString(stOK.Render("           ✓ address") + "\n")
			} else {
				b.WriteString(stWarn.Render("           not an address yet") + "\n")
			}
		}
		if s.step == sendPreparing {
			b.WriteString("\n" + stDim.Render("checking: node synced, fee, balance, inner id…") + "\n")
		} else {
			b.WriteString("\n" + keyHelp("Enter", "check", "Esc", "cancel") + "\n")
		}
	case sendReview, sendSending:
		p := s.plan
		fmt.Fprintf(&b, "%-10s %s %s\n", "To", keys.Address(p.To), stOK.Render("✓"))
		fmt.Fprintf(&b, "%-10s %s CS\n", "Amount", p.Amount.Grouped())
		fmt.Fprintf(&b, "%-10s %s CS %s\n", "Fee", p.Fee.Fixed(6), stDim.Render("(at most "+p.MaxFee.Amount().String()+")"))
		fmt.Fprintf(&b, "%-10s %s\n", "Data", stDim.Render("(none)"))
		b.WriteString("\n")
		if p.Network.IsMainnet() {
			b.WriteString(stBad.Render("This sends real CS on MAINNET.") + "\n")
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
			b.WriteString(stOK.Render(fmt.Sprintf("✓ sent %s CS to %s", s.plan.Amount.Grouped(), keys.Short(keys.Address(s.plan.To)))) + "\n")
			fmt.Fprintf(&b, "%s, inner id %d, fee %s CS\n", where, s.result.InnerID, s.result.Fee.String())
		}
		b.WriteString("\n" + stDim.Render("any key: back") + "\n")
	}
	if s.err != nil {
		b.WriteString("\n" + stBad.Render(oneLine(s.err.Error())) + "\n")
	}
	return stBox.Width(min(m.width-2, 78)).Render(strings.TrimRight(b.String(), "\n"))
}
