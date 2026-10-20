package winpay

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

// jakarta is the timezone Winpay timestamps are expressed in. It falls back to
// a fixed UTC+7 zone when the tzdata is unavailable (minimal containers).
var jakarta = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return loc
	}
	return time.FixedZone("WIB", 7*60*60)
}()

// VA transaction types (virtualAccountTrxType).
const (
	vaTypeOneOff = "c"
	vaTypeOpen   = "o"
	vaTypeClose  = "r"
)

// QRIS service code, echoed on query and reported on status responses.
const serviceCodeQRIS = "47"

// vaChannels lists the bank channel codes documented for Virtual Accounts.
var vaChannels = map[string]bool{
	"BRI": true, "BNI": true, "MANDIRI": true, "PERMATA": true, "BSI": true,
	"MUAMALAT": true, "BCA": true, "CIMB": true, "SINARMAS": true, "BNC": true,
	"MAYBANK": true, "DANAMON": true, "JATIM": true, "BCAPC": true, "MANDIRIPC": true,
}

// retailChannels lists the Winpay modern-retail payment-code channels.
var retailChannels = map[string]bool{
	"INDOMARET": true, "ALFAMART": true, "FASTPAY": true,
}

// ewalletChannels lists the e-wallet channel codes documented for eWallets.
var ewalletChannels = map[string]bool{
	"SC": true, "OVO": true, "DANA": true, "SPAY": true,
}

// channelMethod infers the unified method from a Winpay channel code. It is
// used when parsing callbacks, which carry a channel but no method.
func channelMethod(channel string) provider.Method {
	code := strings.ToUpper(strings.TrimSpace(channel))
	switch {
	case code == "":
		return ""
	case code == "QRIS":
		return provider.MethodQRIS
	case ewalletChannels[code]:
		return provider.MethodEWallet
	case vaChannels[code]:
		return provider.MethodVirtualAccount
	case retailChannels[code]:
		return provider.MethodRetailOutlet
	default:
		return ""
	}
}

// mapTransactionStatus translates Winpay's "latestTransactionStatus" code.
//
// The code "07" is overloaded by Winpay: QRIS documents it as "not found" while
// eWallet documents it as "unpaid", so callers pass the status that applies to
// the flow being handled.
func mapTransactionStatus(code string, notFound provider.Status) provider.Status {
	switch strings.TrimSpace(code) {
	case "00":
		return provider.StatusPaid
	case "01", "02", "03":
		return provider.StatusPending
	case "04":
		return provider.StatusRefunded
	case "05":
		return provider.StatusCancelled
	case "06":
		return provider.StatusFailed
	case "07":
		return notFound
	default:
		return provider.StatusUnknown
	}
}

// mapVAStatus translates the Virtual Account "paymentFlagStatus" code:
// 00 paid, 01 unpaid, 02 check.
func mapVAStatus(code string) provider.Status {
	switch strings.TrimSpace(code) {
	case "00":
		return provider.StatusPaid
	case "01", "02":
		return provider.StatusPending
	default:
		return provider.StatusUnknown
	}
}

// parseAmount converts a Winpay decimal amount string ("25000.00", "10000")
// into the contract's integer smallest-unit amount (rupiah for IDR).
//
// Sub-unit fractions are rejected rather than silently rounded: IDR has no
// practical fractional unit, so encountering one means we are looking at
// something we do not understand.
func parseAmount(value string) (provider.Amount, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, nil
	}
	units, fraction, _ := strings.Cut(raw, ".")
	units = strings.TrimLeft(units, "+")
	n, err := strconv.ParseInt(units, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("winpay: invalid amount %q: %w", value, err)
	}
	if strings.Trim(fraction, "0") != "" {
		return 0, fmt.Errorf("winpay: amount %q has sub-unit precision", value)
	}
	return n, nil
}

// formatAmount renders an integer rupiah amount in the "25000.00" shape Winpay
// expects on requests.
func formatAmount(a provider.Amount) string {
	return fmt.Sprintf("%d.00", a)
}

// parseTime parses the ISO-8601 timestamps Winpay uses ("+07:00", "+00:00" or
// "Z"). It returns nil when the value is empty or unparseable so callers can
// treat the field as optional.
func parseTime(value string) *time.Time {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{iso8601Layout, time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t
		}
	}
	return nil
}

// paidAtIfPaid keeps a settlement timestamp only for actually settled payments.
func paidAtIfPaid(status provider.Status, t *time.Time) *time.Time {
	if status == provider.StatusPaid {
		return t
	}
	return nil
}

// classifyCode maps a Winpay response code onto a contract sentinel so the core
// engine can react with errors.Is.
func classifyCode(code string) error {
	switch {
	case strings.HasPrefix(code, "401"), strings.HasPrefix(code, "403"):
		return provider.ErrUnauthorized
	case strings.HasPrefix(code, "404"):
		return provider.ErrNotFound
	case strings.HasPrefix(code, "409"):
		return provider.ErrDuplicateReference
	case strings.HasPrefix(code, "400"):
		return provider.ErrInvalidRequest
	default:
		return provider.ErrGateway
	}
}

// sanitizeName reduces a name to the character set Winpay accepts for account
// and customer names (letters, digits, underscore and hyphen), collapsing runs
// of whitespace.
func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// accountName builds a name that satisfies Winpay's 5-24 character rule from a
// customer name, falling back to the merchant reference.
func accountName(customerName, reference string) string {
	name := sanitizeName(customerName)
	if len(name) < 5 {
		name = sanitizeName("OPENPAY " + reference)
	}
	if len(name) < 5 {
		name = "OPENPAY CUSTOMER"
	}
	if len(name) > 24 {
		name = strings.TrimSpace(name[:24])
	}
	return name
}

// firstNonEmpty returns the first argument that is not blank.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// externalID returns a value for the X-EXTERNAL-ID header. Winpay requires it
// to be unique per merchant per day, which a nanosecond timestamp guarantees.
func externalID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}

// truncate bounds an error message taken from a raw gateway response.
func truncate(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
