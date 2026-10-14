package doku

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

// jakarta is the timezone DOKU timestamps are expressed in. It falls back to a
// fixed UTC+7 zone when the tzdata is unavailable (minimal containers).
var jakarta = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return loc
	}
	return time.FixedZone("WIB", 7*60*60)
}()

// VA transaction types (virtualAccountTrxType).
const (
	vaTypeClosed = "C"
	vaTypeOpen   = "O"
	vaTypeBill   = "V"
)

// QRIS service code, echoed on query and reported on status responses.
const serviceCodeQRIS = "47"

// Debit status service code.
const serviceCodeDebit = "55"

// vaBank describes the per-bank create-va endpoint path and the
// additionalInfo.channel enum DOKU expects for that bank.
type vaBank struct {
	path    string
	channel string
}

// vaBanks maps the unified VA channel code onto the bank-specific create-va
// path and channel enum. DOKU does not use one unified VA endpoint: the path
// differs per issuing bank. Banks not listed here must be reached through the
// va_path config override.
var vaBanks = map[string]vaBank{
	"BCA":      {"/virtual-accounts/bi-snap-va/v1.1/transfer-va/create-va", "VIRTUAL_ACCOUNT_BCA"},
	"BRI":      {"/virtual-accounts/bi-snap-va/v1.1/transfer-va/create-va", "VIRTUAL_ACCOUNT_BRI"},
	"BNI":      {"/virtual-accounts/bi-snap-va/v1/transfer-va/create-va", "VIRTUAL_ACCOUNT_BNI"},
	"BNC":      {"/virtual-accounts/bi-snap-va/v1/transfer-va/create-va", "VIRTUAL_ACCOUNT_BNC"},
	"MANDIRI":  {"/bi-snap-va/mandiri/v1/transfer-va/create-va", "VIRTUAL_ACCOUNT_BANK_MANDIRI"},
	"PERMATA":  {"/bi-snap-va/permata/v1/transfer-va/create-va", "VIRTUAL_ACCOUNT_PERMATA"},
	"CIMB":     {"/bi-snap-va/cimb/v1/transfer-va/create-va", "VIRTUAL_ACCOUNT_BANK_CIMB"},
	"BSI":      {"/bi-snap-va/bsi/v1/transfer-va/create-va", "VIRTUAL_ACCOUNT_BSI"},
	"DANAMON":  {"/bi-snap-va/danamon/v1/transfer-va/create-va", "VIRTUAL_ACCOUNT_BANK_DANAMON"},
	"MAYBANK":  {"/bi-snap-va/maybank/v1/transfer-va/create-va", "VIRTUAL_ACCOUNT_MAYBANK"},
	"SINARMAS": {"/bi-snap-va/sinarmas/v1/transfer-va/create-va", "VIRTUAL_ACCOUNT_SINARMAS"},
	"BTN":      {"/bi-snap-va/btn/v1/transfer-va/create-va", "VIRTUAL_ACCOUNT_BTN"},
	"BJB":      {"/virtual-accounts/bi-snap-va/v1.1/transfer-va/create-va", "VIRTUAL_ACCOUNT_BANK_BJB"},
}

// ewalletChannels maps the unified e-wallet channel code onto the DOKU
// direct-debit channel enum, the payment-host-to-host API version, and whether
// the request uses urlParam (DANA) or urlParams (ShopeePay).
type ewalletChannel struct {
	channel string
	version int
}

var ewalletChannels = map[string]ewalletChannel{
	"DANA":      {"EMONEY_DANA_SNAP", 1},
	"SHOPEEPAY": {"EMONEY_SHOPEE_PAY_SNAP", 2},
}

// mapTransactionStatus translates DOKU's "latestTransactionStatus" code onto a
// unified [provider.Status].
//
//	00 Success, 01 Initiated, 02 Paying, 03 Pending, 04 Refunded,
//	05 Canceled, 06 Failed, 07 Not Found
func mapTransactionStatus(code string) provider.Status {
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
		return provider.StatusFailed
	default:
		return provider.StatusUnknown
	}
}

// mapVAStatus derives a unified status from the VA status response. DOKU's VA
// status endpoint does not return a numeric code; it returns a
// paymentFlagReason object whose text says "Pending", "Paid", etc.
func mapVAStatus(data vaStatusData) provider.Status {
	reason := strings.ToLower(data.PaymentFlagReason.English + " " + data.PaymentFlagReason.Indonesia)
	switch {
	case strings.Contains(reason, "paid") || strings.Contains(reason, "success") || strings.Contains(reason, "settlement"):
		return provider.StatusPaid
	case strings.Contains(reason, "cancel"):
		return provider.StatusCancelled
	case strings.Contains(reason, "fail"):
		return provider.StatusFailed
	case strings.Contains(reason, "expire"):
		return provider.StatusExpired
	case data.PaymentRequestID != "" || data.PaidAmount != nil:
		return provider.StatusPaid
	default:
		return provider.StatusPending
	}
}

// parseAmount converts a DOKU decimal amount string ("10000.00") into the
// unified amount. DOKU expresses amounts in rupiah with two decimal places
// (the decimals are always .00 for IDR), so "10000.00" is 10000 rupiah.
func parseAmount(value string) (provider.Amount, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, fmt.Errorf("doku: empty amount")
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("doku: invalid amount %q: %w", raw, err)
	}
	return provider.Amount(f + 0.5), nil
}

// formatAmount renders an integer minor-unit amount as the 2-decimal string
// DOKU expects, e.g. 10000 -> "10000.00".
func formatAmount(a provider.Amount) string {
	return strconv.FormatInt(a, 10) + ".00"
}

// classifyCode maps a DOKU response code onto a contract sentinel so the core
// engine can react with errors.Is. DOKU response codes are HTTP status +
// service code + case code; the leading digits follow HTTP conventions.
func classifyCode(code string) error {
	switch {
	case strings.HasPrefix(code, "200"):
		return nil
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

// accountName builds a name that satisfies DOKU's virtual-account name rules
// from a customer name, falling back to the merchant reference.
func accountName(customerName, reference string) string {
	name := strings.Join(strings.Fields(customerName), " ")
	if name == "" {
		name = "OPENPAY " + reference
	}
	if len(name) > 60 {
		name = strings.TrimSpace(name[:60])
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

// externalID returns a value for the X-EXTERNAL-ID header. DOKU requires it to
// be unique per merchant per day, which a nanosecond timestamp guarantees.
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

// parseTime parses a DOKU timestamp. It returns nil when the value is empty or
// unparseable so callers can treat the field as optional.
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
