package main

import (
	"context"
	"errors"
	"fmt"
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
	list, err := a.w.Node.TokenBalances(ctx, pub)
	if errors.Is(err, node.ErrNoTokenCache) {
		return fmt.Errorf("%w; meanwhile 'token balance TOKEN ACCOUNT' reads one balance from the contract", err)
	}
	if err != nil {
		return err
	}
	type row struct {
		Token   string `json:"token"`
		Code    string `json:"code"`
		Name    string `json:"name"`
		Balance string `json:"balance"`
	}
	rows := []row{}
	for _, b := range list {
		rows = append(rows, row{keys.Address(b.Token), b.Code, b.Name, b.Balance})
	}
	if a.json {
		return a.printJSON(map[string]any{"address": keys.Address(pub), "tokens": rows})
	}
	if len(rows) == 0 {
		fmt.Fprintf(a.out, "%s holds no tokens\n", keys.Address(pub))
	}
	for _, r := range rows {
		fmt.Fprintf(a.out, "%-8s %24s  %s  %s\n", printable(r.Code), r.Balance, r.Token, printable(r.Name))
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
		return usagef("token: info, balance or send")
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
