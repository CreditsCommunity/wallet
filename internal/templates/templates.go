// Package templates holds the example contracts the wallet offers as templates: the node's
// samples/contracts (CreditsCommunity/node, AGPL-3.0, by the community node's authors), embedded
// as they are at the commit below. ci/privnet/e2e.sh checks them against the node repository at
// the same commit, and deploys and calls Counter, TestToken and TipJar on a private network.
package templates

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/CreditsCommunity/wallet/internal/echo"
)

// Source is where the templates come from: the node repository and the commit they were taken at.
const Source = "CreditsCommunity/node samples/contracts at 079e788d521dbb255c77c8b4abeeea0e4c60fcef (AGPL-3.0)"

//go:embed java/*.java
var files embed.FS

// Template is one example contract.
type Template struct {
	Name  string // the contract's class name, also the template's name
	About string
}

var list = []Template{
	{"Counter", "the simplest contract: state, an owner, calls that change the state, getters, a payable() that refuses CS"},
	{"TestToken", "Community Test Token (CTT), FOR TESTS ONLY: a token of standard 4 the node's token API lists"},
	{"Token", "a token template to start a real token from: amounts checked against the decimals, approve, a sale through payable()"},
	{"TipJar", "a payable contract: records who paid how much; the owner withdraws with sendTransaction()"},
	{"Faucet", "a contract that reads its own balance, and why its result depends on when it runs"},
}

// List returns the templates, in the order of the node's samples/contracts README.
func List() []Template { return append([]Template(nil), list...) }

// Names returns the template names, sorted.
func Names() []string {
	out := make([]string, 0, len(list))
	for _, t := range list {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// Get returns a template's Java source by its name (case does not matter).
func Get(name string) (string, Template, error) {
	for _, t := range list {
		if strings.EqualFold(t.Name, name) {
			b, err := files.ReadFile("java/" + t.Name + ".java")
			return string(b), t, err
		}
	}
	return "", Template{}, fmt.Errorf("no template %s: the templates are %s", echo.Quote(name), strings.Join(Names(), ", "))
}
