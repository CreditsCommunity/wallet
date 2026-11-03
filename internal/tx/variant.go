package tx

import (
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/CreditsCommunity/wallet/internal/echo"
	"github.com/CreditsCommunity/wallet/thrift/general"
)

// Contract arguments are typed text converted to the Variant of the type the method declares (the
// executor refuses a call whose Variants do not match), as the node's sample clients do.

var decimalText = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// Variant converts text to the Variant the executor matches with a declared Java type: String,
// BigDecimal, int, long, boolean, double and their wrappers, byte[] as hex.
func Variant(declared, text string) (*general.Variant, error) {
	v := general.NewVariant()
	var err error
	switch declared {
	case "java.lang.String":
		s := text
		v.VString = &s
	case "java.math.BigDecimal":
		if !decimalText.MatchString(text) {
			err = errors.New("not a number")
		}
		d := strings.TrimPrefix(text, "+")
		v.VBigDecimal = &d
	case "int", "java.lang.Integer":
		var n int64
		if n, err = strconv.ParseInt(text, 10, 32); err == nil {
			i := int32(n)
			if declared == "int" {
				v.VInt = &i
			} else {
				v.VIntBox = &i
			}
		}
	case "long", "java.lang.Long":
		var n int64
		if n, err = strconv.ParseInt(text, 10, 64); err == nil {
			if declared == "long" {
				v.VLong = &n
			} else {
				v.VLongBox = &n
			}
		}
	case "boolean", "java.lang.Boolean":
		var b bool
		switch strings.ToLower(text) {
		case "true":
			b = true
		case "false":
		default:
			err = errors.New("not a boolean")
		}
		if err == nil {
			if declared == "boolean" {
				v.VBoolean = &b
			} else {
				v.VBooleanBox = &b
			}
		}
	case "double", "java.lang.Double":
		var f float64
		if f, err = strconv.ParseFloat(text, 64); err == nil {
			if declared == "double" {
				v.VDouble = &f
			} else {
				v.VDoubleBox = &f
			}
		}
	case "byte[]":
		var b []byte
		if b, err = hex.DecodeString(text); err == nil {
			v.VByteArray = b
		}
	default:
		return nil, fmt.Errorf("arguments of type %s are not supported", declared)
	}
	if err != nil {
		return nil, fmt.Errorf("%s is not a %s", echo.Quote(text), declared)
	}
	return v, nil
}

func floatText(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, "eIN.") {
		s += ".0"
	}
	return s
}

// Show renders a returned Variant as text, as the node's sample clients do; collections (a
// contract's state) as [a, b] and {k: v}, sorted, at most maxShown elements each and maxDepth deep.
// The text is the contract's: callers show it through echo.Printable.
func Show(v *general.Variant) string { return show(v, 0) }

const (
	maxShown = 50
	maxDepth = 4
)

func showList(items []*general.Variant, depth int, open, close string) string {
	if depth >= maxDepth {
		return open + "…" + close
	}
	parts := make([]string, 0, min(len(items), maxShown)+1)
	for i, x := range items {
		if i == maxShown {
			parts = append(parts, fmt.Sprintf("… %d more", len(items)-maxShown))
			break
		}
		parts = append(parts, show(x, depth+1))
	}
	return open + strings.Join(parts, ", ") + close
}

func showMap(m map[*general.Variant]*general.Variant, depth int) string {
	if depth >= maxDepth {
		return "{…}"
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, show(k, depth+1)+": "+show(v, depth+1))
	}
	sort.Strings(parts)
	if len(parts) > maxShown {
		parts = append(parts[:maxShown], fmt.Sprintf("… %d more", len(parts)-maxShown))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func show(v *general.Variant, depth int) string {
	switch {
	case v != nil && v.VArray != nil:
		return showList(v.VArray, depth, "[", "]")
	case v != nil && v.VList != nil:
		return showList(v.VList, depth, "[", "]")
	case v != nil && v.VSet != nil:
		sorted := append([]*general.Variant(nil), v.VSet...)
		sort.Slice(sorted, func(i, j int) bool { return show(sorted[i], maxDepth) < show(sorted[j], maxDepth) })
		return showList(sorted, depth, "{", "}")
	case v != nil && v.VMap != nil:
		return showMap(v.VMap, depth)
	case v != nil && v.VAmount != nil:
		return fmt.Sprintf("%d.%018d", v.VAmount.Integral, v.VAmount.Fraction)
	case v != nil && v.VObject != nil:
		return fmt.Sprintf("(object of %d bytes)", len(v.VObject.Instance))
	}
	switch {
	case v == nil:
		return "(nothing)"
	case v.VBoolean != nil:
		return strconv.FormatBool(*v.VBoolean)
	case v.VBooleanBox != nil:
		return strconv.FormatBool(*v.VBooleanBox)
	case v.VString != nil:
		return *v.VString
	case v.VBigDecimal != nil:
		return *v.VBigDecimal
	case v.VByte != nil:
		return strconv.Itoa(int(*v.VByte))
	case v.VByteBox != nil:
		return strconv.Itoa(int(*v.VByteBox))
	case v.VShort != nil:
		return strconv.Itoa(int(*v.VShort))
	case v.VShortBox != nil:
		return strconv.Itoa(int(*v.VShortBox))
	case v.VInt != nil:
		return strconv.Itoa(int(*v.VInt))
	case v.VIntBox != nil:
		return strconv.Itoa(int(*v.VIntBox))
	case v.VLong != nil:
		return strconv.FormatInt(*v.VLong, 10)
	case v.VLongBox != nil:
		return strconv.FormatInt(*v.VLongBox, 10)
	case v.VFloat != nil:
		return floatText(float64(*v.VFloat))
	case v.VFloatBox != nil:
		return floatText(float64(*v.VFloatBox))
	case v.VDouble != nil:
		return floatText(*v.VDouble)
	case v.VDoubleBox != nil:
		return floatText(*v.VDoubleBox)
	case v.VByteArray != nil:
		return hex.EncodeToString(v.VByteArray)
	case v.VVoid != nil:
		return "void"
	case v.VNull != nil:
		return "null"
	}
	return "(nothing)"
}
