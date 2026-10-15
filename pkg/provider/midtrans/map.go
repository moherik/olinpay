package midtrans

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/moherik/openpay.id/pkg/provider"
)

// jakarta is the timezone Midtrans timestamps are expressed in. It falls back
// to a fixed UTC+7 zone when the tzdata is unavailable (minimal containers).
var jakarta = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return loc
	}
	return time.FixedZone("WIB", 7*60*60)
}()

// QRIS service code, reported on qr-mpm-query requests and responses.
const qrisServiceCode = "47"

// Debit (e-wallet) service code, reported on debit/status requests.
const debitServiceCode = "54"

// maxExpiry caps the requested payment lifetime.
const maxExpiry = 180 * 24 * time.Hour

// vaBanks maps the unified VA channel code onto the lowercase bank slug
// Midtrans expects in additionalInfo.bank.
var vaBanks = map[string]string{
	"PERMATA": "permata",
	"BCA":     "bca",
	"MANDIRI": "mandiri",
	"CIMB":    "cimb",
	"BNI":     "bni",
	"BRI":     "bri",
	"DANAMON": "danamon",
	"BSI":     "bsi",
	"SEABANK": "seabank",
	"SAQU":    "saqu",
}

// ewalletChannels maps the unified e-wallet channel code onto the payMethod /
// payOption enum Midtrans expects on the direct-debit request.
var ewalletChannels = map[string]string{
	"GOPAY":     "GOPAY",
	"SHOPEEPAY": "SHOPEEPAY",
	"DANA":      "DANA",
}

// mapTransactionStatus translates Midtrans' SNAP numeric transaction status
// onto a unified [provider.Status]:
//
//	00 Success, 01 Initiated, 02 Paying, 03 Pending, 04 Refunded,
//	05 Canceled, 06 Failed, 07 Not Found, 08 Expiry, 09 Rejected
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
	case "06", "07", "09":
		return provider.StatusFailed
	case "08":
		return provider.StatusExpired
	default:
		return provider.StatusUnknown
	}
}

// mapVAStatus derives a unified status from the VA status response. It prefers
// the numeric paymentFlagStatus and falls back to the textual reason when the
// code is absent.
func mapVAStatus(data vaStatusData) provider.Status {
	if status := mapTransactionStatus(data.PaymentFlagStatus); status != provider.StatusUnknown {
		return status
	}
	reason := strings.ToLower(data.PaymentFlagReason.English + " " + data.PaymentFlagReason.Indonesia)
	switch {
	case strings.Contains(reason, "paid"), strings.Contains(reason, "success"), strings.Contains(reason, "berhasil"):
		return provider.StatusPaid
	case strings.Contains(reason, "cancel"):
		return provider.StatusCancelled
	case strings.Contains(reason, "fail"), strings.Contains(reason, "reject"):
		return provider.StatusFailed
	case strings.Contains(reason, "expire"):
		return provider.StatusExpired
	default:
		return provider.StatusPending
	}
}

// parseAmount converts a Midtrans decimal amount string ("10000.00") into the
// unified integer smallest-unit amount (rupiah for IDR). Sub-unit fractions are
// rejected rather than silently rounded: IDR has no practical fractional unit.
func parseAmount(value string) (provider.Amount, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, fmt.Errorf("midtrans: empty amount")
	}
	units, fraction, _ := strings.Cut(raw, ".")
	units = strings.TrimLeft(units, "+")
	n, err := strconv.ParseInt(units, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("midtrans: invalid amount %q: %w", value, err)
	}
	if strings.Trim(fraction, "0") != "" {
		return 0, fmt.Errorf("midtrans: amount %q has sub-unit precision", value)
	}
	return n, nil
}

// amountOr parses the gateway amount, falling back to the value captured at
// creation time when the field is absent or malformed.
func amountOr(payload *amountPayload, fallback provider.Amount) provider.Amount {
	if payload != nil {
		if parsed, err := parseAmount(payload.Value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

// formatAmount renders an integer minor-unit amount as the 2-decimal string
// Midtrans expects, e.g. 10000 -> "10000.00".
func formatAmount(a provider.Amount) string {
	return strconv.FormatInt(a, 10) + ".00"
}

// amountPayloadOf builds the request amount object for an IDR amount.
func amountPayloadOf(a provider.Amount) *amountPayload {
	return &amountPayload{Value: formatAmount(a), Currency: string(provider.IDR)}
}

// classifyCode maps a Midtrans SNAP response code onto a contract sentinel so
// the core engine can react with errors.Is. SNAP response codes are
// HTTP-status + service-code + case-code; the leading three digits follow HTTP
// conventions.
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
	case strings.HasPrefix(code, "400"), strings.HasPrefix(code, "422"):
		return provider.ErrInvalidRequest
	default:
		return provider.ErrGateway
	}
}

// accountName builds a virtual-account holder name, falling back to the merchant
// reference so the field is never blank.
func accountName(customerName, reference string) string {
	name := strings.Join(strings.Fields(customerName), " ")
	if name == "" {
		name = "OPENPAY " + reference
	}
	if len(name) > 255 {
		name = strings.TrimSpace(name[:255])
	}
	return name
}

// splitName divides a display name into first and last name at the first
// whitespace, matching Midtrans' customerDetails shape.
func splitName(name string) (first, last string) {
	fields := strings.Fields(name)
	switch len(fields) {
	case 0:
		return "", ""
	case 1:
		return fields[0], ""
	default:
		return fields[0], strings.Join(fields[1:], " ")
	}
}

// customerNo derives a deterministic, digits-only customer number from the
// merchant reference when the caller does not supply one. Midtrans composes the
// VA number from partnerServiceId + customerNo and trims or zero-pads it to the
// issuing bank's length rules, so any short numeric value is acceptable.
func customerNo(reference string) string {
	sum := sha256.Sum256([]byte(reference))
	n := binary.BigEndian.Uint64(sum[:8]) % 10_000_000_000
	return fmt.Sprintf("%010d", n)
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

// externalID returns a fresh value for the X-EXTERNAL-ID header. Midtrans
// treats it as a per-request idempotency key: it must be numeric, unique for the
// day, and is later replayed as the originalExternalId of status/cancel calls.
// The value combines the wall clock with random digits so it stays unique across
// processes and instances.
func externalID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	n := binary.BigEndian.Uint32(buf[:])
	return fmt.Sprintf("%d%09d", time.Now().UnixNano(), n%1_000_000_000)
}

// truncate bounds an error message taken from a raw gateway response.
func truncate(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// parseTime parses a Midtrans SNAP timestamp. It returns nil when the value is
// empty or unparseable so callers can treat the field as optional.
func parseTime(value string) *time.Time {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{iso8601Layout, time.RFC3339Nano, time.RFC3339} {
		if t, err := time.ParseInLocation(layout, raw, jakarta); err == nil {
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
