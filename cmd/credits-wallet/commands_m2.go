package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/CreditsCommunity/wallet/internal/keys"
	"github.com/CreditsCommunity/wallet/internal/node"
	"github.com/CreditsCommunity/wallet/internal/wallet"
)

// ---- delegations -------------------------------------------------------------------------------

func (a *app) cmdDelegate(ctx context.Context, cmd string, args []string) error {
	fs := newFlagSet(a, cmd)
	yes := fs.Bool("yes", false, "send without asking (scripts)")
	wait := fs.Int("wait", 120, "seconds to follow the transaction until it is in a block")
	until := fs.String("until", "", "delegate until this UTC date")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if len(pos) != 3 {
		return usagef("%s <account> <node> <amount>", cmd)
	}
	var plan *wallet.SendPlan
	if cmd == "undelegate" {
		if *until != "" {
			return usagef("undelegate has no --until: a delegation with a time limit ends by itself")
		}
		plan, err = a.w.PrepareUndelegate(ctx, pos[0], pos[1], pos[2])
	} else {
		var t time.Time
		if *until != "" {
			if t, err = wallet.ParseUntil(*until); err != nil {
				return err
			}
		}
		plan, err = a.w.PrepareDelegate(ctx, pos[0], pos[1], pos[2], t)
	}
	if err != nil {
		return err
	}
	return a.confirmAndSend(ctx, plan, *yes, *wait)
}

type delegationRow struct {
	Wallet string     `json:"wallet"`
	Amount string     `json:"amount_cs"`
	Until  *time.Time `json:"until,omitempty"`
	From   *time.Time `json:"from,omitempty"`
}

func delegationRows(list []node.Delegation) []delegationRow {
	rows := []delegationRow{}
	for _, d := range list {
		r := delegationRow{Wallet: keys.Address(d.Wallet), Amount: d.Amount.String()}
		if !d.Until.IsZero() {
			u := d.Until
			r.Until = &u
		}
		if !d.From.IsZero() {
			f := d.From
			r.From = &f
		}
		rows = append(rows, r)
	}
	return rows
}

func (a *app) cmdDelegations(ctx context.Context, args []string) error {
	fs := newFlagSet(a, "delegations")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("delegations <account|address>")
	}
	_, pub, err := a.w.Account(pos[0])
	if err != nil {
		return err
	}
	d, err := a.w.Node.Delegations(ctx, pub)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(map[string]any{"address": keys.Address(pub), "delegated_out_cs": d.OutTotal.String(),
			"delegated_in_cs": d.InTotal.String(), "made": delegationRows(d.Out), "received": delegationRows(d.In)})
	}
	fmt.Fprintf(a.out, "%s\n", keys.Address(pub))
	for _, part := range []struct {
		title string
		total string
		list  []node.Delegation
		arrow string
	}{{"made", d.OutTotal.Grouped(), d.Out, "→"}, {"received", d.InTotal.Grouped(), d.In, "←"}} {
		fmt.Fprintf(a.out, "delegations %s: %s CS in %s\n", part.title, part.total, plural(len(part.list), "delegation"))
		for _, x := range part.list {
			fmt.Fprintf(a.out, "  %s %s  %s CS%s\n", part.arrow, keys.Address(x.Wallet), x.Amount.Grouped(), untilText(x.Until))
		}
	}
	return nil
}

// ---- tokens ------------------------------------------------------------------------------------

func (a *app) cmdTokens(ctx context.Context, args []string) error {
	fs := newFlagSet(a, "tokens")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("tokens <account|address>")
	}
	_, pub, err := a.w.Account(pos[0])
	if err != nil {
		return err
	}
	l, err := a.w.Tokens(ctx, pub)
	if err != nil {
		return err
	}
	type row struct {
		Token    string   `json:"token"`
		Code     string   `json:"code"`
		Name     string   `json:"name"`
		Decimals *int     `json:"decimals,omitempty"`
		Balance  string   `json:"balance"`
		Added    bool     `json:"added,omitempty"`
		Warnings []string `json:"warnings,omitempty"`
		Error    string   `json:"error,omitempty"`
	}
	rows := []row{}
	for _, h := range l.Holdings {
		r := row{Token: keys.Address(h.Token), Code: h.Code, Name: h.Name, Balance: h.Balance, Added: h.Added, Warnings: h.Warnings}
		if h.Decimals >= 0 {
			d := h.Decimals
			r.Decimals = &d
		}
		if h.Err != nil {
			r.Error = h.Err.Error()
		}
		rows = append(rows, r)
	}
	if a.json {
		out := map[string]any{"address": keys.Address(pub), "tokens": rows, "source": string(l.Source), "source_text": l.Describe,
			"complete": l.Note == ""}
		if l.Note != "" {
			out["note"] = l.Note
			out["zero_balance"] = l.Zero
			out["transactions_scanned"] = l.Scanned
		}
		return a.printJSON(out)
	}
	fmt.Fprintf(os.Stderr, "tokens from %s\n", l.Describe)
	if l.Note != "" {
		fmt.Fprintf(os.Stderr, "%s: %s; the node keeps no token cache ([api] tokens_cache=true in its config.ini lists them all)\n",
			"note", l.Note)
		if l.Capped {
			fmt.Fprintf(os.Stderr, "note: only the latest %d transactions were read\n", l.Scanned)
		}
		if l.Zero > 0 {
			fmt.Fprintf(os.Stderr, "%s with a balance of 0 not shown\n", plural(l.Zero, "token"))
		}
	}
	if l.LocalErr != nil {
		fmt.Fprintf(os.Stderr, "warning: the local token list: %s\n", printable(l.LocalErr.Error()))
	}
	if len(rows) == 0 {
		fmt.Fprintf(a.out, "%s holds no tokens%s\n", keys.Address(pub), map[bool]string{true: " that were found", false: ""}[l.Note != ""])
	}
	for _, r := range rows {
		if r.Error != "" {
			fmt.Fprintf(a.out, "%-8s %24s  %s  %s\n", printable(r.Code), "?", r.Token, printable(r.Error))
			continue
		}
		fmt.Fprintf(a.out, "%-8s %24s  %s  %s\n", printable(r.Code), printable(r.Balance), r.Token, printable(r.Name))
		for _, w := range r.Warnings {
			fmt.Fprintf(a.out, "         ! %s\n", printable(w))
		}
	}
	return nil
}

func (a *app) cmdToken(ctx context.Context, args []string) error {
	fs := newFlagSet(a, "token")
	yes := fs.Bool("yes", false, "send without asking (scripts)")
	wait := fs.Int("wait", 120, "seconds to follow the call until it is in a block")
	pos, err := a.commandArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usagef("token: info, balance, add, remove or send")
	}
	sub, rest := pos[0], pos[1:]
	switch {
	case sub == "info" && len(rest) == 1:
		return a.tokenInfo(ctx, rest[0])
	case sub == "balance" && len(rest) == 2:
		token, err := keys.ParseAddress(rest[0])
		if err != nil {
			return fmt.Errorf("token: %w", err)
		}
		_, pub, err := a.w.Account(rest[1])
		if err != nil {
			return err
		}
		tp, _, err := a.w.TokenMethods(ctx, pub, token)
		if err != nil {
			return err
		}
		bal, err := a.w.TokenBalanceOf(ctx, token, pub)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(map[string]any{"token": rest[0], "code": tp.Code, "address": keys.Address(pub), "balance": bal})
		}
		fmt.Fprintf(a.out, "%s %s\n", printable(bal), printable(tp.Code))
		return nil
	case sub == "add" && len(rest) == 2:
		_, pub, err := a.w.Account(rest[0])
		if err != nil {
			return err
		}
		h, err := a.w.AddToken(ctx, pub, rest[1])
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(map[string]any{"address": keys.Address(pub), "token": keys.Address(h.Token), "code": h.Code,
				"name": h.Name, "decimals": h.Decimals, "balance": h.Balance, "warnings": h.Warnings})
		}
		fmt.Fprintf(a.out, "added %s %s (%s), balance %s\n", keys.Address(h.Token), printable(h.Code), printable(h.Name), printable(h.Balance))
		for _, w := range h.Warnings {
			fmt.Fprintf(a.out, "! %s\n", printable(w))
		}
		return nil
	case sub == "remove" && len(rest) == 2:
		_, pub, err := a.w.Account(rest[0])
		if err != nil {
			return err
		}
		removed, err := a.w.RemoveToken(pub, rest[1])
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(map[string]any{"address": keys.Address(pub), "token": rest[1], "removed": removed})
		}
		if !removed {
			fmt.Fprintf(a.out, "%s was not in the account's token list\n", printable(rest[1]))
		} else {
			fmt.Fprintf(a.out, "removed %s from the account's token list\n", printable(rest[1]))
		}
		return nil
	case sub == "send" && len(rest) == 4:
		plan, err := a.w.PrepareTokenSend(ctx, rest[0], rest[1], rest[2], rest[3])
		if err != nil {
			return err
		}
		return a.confirmAndSend(ctx, plan, *yes, *wait)
	}
	return usagef("token %s: wrong arguments", sub)
}

func (a *app) tokenInfo(ctx context.Context, text string) error {
	token, err := keys.ParseAddress(text)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}
	tp, _, err := a.w.TokenMethods(ctx, nil, token)
	if err != nil {
		return err
	}
	info, infoErr := a.w.Node.TokenInfo(ctx, token)
	if a.json {
		out := map[string]any{"token": text, "code": tp.Code, "name": tp.Name, "decimals": tp.Decimals}
		if infoErr == nil {
			out["total_supply"], out["owner"], out["holders"], out["transfers"], out["standard"] =
				info.TotalSupply, keys.Address(info.Owner), info.Holders, info.Transfers, info.Standard
		}
		return a.printJSON(out)
	}
	fmt.Fprintf(a.out, "token     : %s\ncode      : %s\nname      : %s\ndecimals  : %d\n", text, printable(tp.Code), printable(tp.Name), tp.Decimals)
	if infoErr != nil {
		fmt.Fprintf(a.out, "(supply and holders: %v)\n", infoErr)
		return nil
	}
	fmt.Fprintf(a.out, "supply    : %s\nowner     : %s\nholders   : %d\ntransfers : %d\nstandard  : %d\n",
		printable(info.TotalSupply), keys.Address(info.Owner), info.Holders, info.Transfers, info.Standard)
	return nil
}
