package midtrans

import (
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

// midtransTimeLayout is the "2021-06-15 18:45:28" shape Midtrans uses for
// transaction_time, settlement_time and expiry_time.
const midtransTimeLayout = "2006-01-02 15:04:05"

// vaBanks maps the unified VA channel codes onto Midtrans' bank_transfer bank
// identifiers. Mandiri is handled separately because it uses the echannel
// (Mandiri Bill Payment) product rather than bank_transfer.
var vaBanks = map[string]string{
	"BCA":     "bca",
	"BNI":     "bni",
	"BRI":     "bri",
	"PERMATA": "permata",
	"CIMB":    "cimb",
}

// ewalletTypes maps e-wallet channel codes onto Midtrans payment types.
var ewalletTypes = map[string]string{
	"GOPAY":     paymentTypeGoPay,
	"SHOPEEPAY": paymentTypeShopeePay,
}

// cstoreStores maps over-the-counter channel codes onto Midtrans store codes.
var cstoreStores = map[string]string{
	"ALFAMART":  "alfamart",
	"INDOMARET": "indomaret",
}

// channelMandiri is the unified channel for Mandiri Bill Payment.
const channelMandiri = "MANDIRI"

// channelAkulaku is the unified channel for the Akulaku pay-later product.
const channelAkulaku = "AKULAKU"

// channelCard is the unified channel for card payments.
const channelCard = "CARD"

// mapTransactionStatus translates Midtrans' "transaction_status" (plus
// "fraud_status" for cards) into a unified [provider.Status].
func mapTransactionStatus(status, fraud string) provider.Status {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "capture":
		// A capture flagged for manual review is not settled yet.
		if strings.EqualFold(strings.TrimSpace(fraud), "challenge") {
			return provider.StatusPending
		}
		return provider.StatusPaid
	case "settlement":
		return provider.StatusPaid
	case "pending", "authorize":
		return provider.StatusPending
	case "deny":
		return provider.StatusFailed
	case "cancel":
		return provider.StatusCancelled
	case "expire":
		return provider.StatusExpired
	case "refund", "partial_refund":
		return provider.StatusRefunded
	case "chargeback", "partial_chargeback":
		return provider.StatusFailed
	default:
		return provider.StatusUnknown
	}
}

// parseAmount converts a Midtrans decimal amount string ("25000.00", "10000")
// into the contract's integer smallest-unit amount (rupiah for IDR).
//
// Midtrans always renders amounts with two decimal places. Sub-unit fractions
// are rejected rather than silently rounded: IDR has no practical fractional
// unit, so encountering one means we are looking at something we cannot map.
func parseAmount(value string) (provider.Amount, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, nil
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
func amountOr(value string, fallback provider.Amount) provider.Amount {
	if parsed, err := parseAmount(value); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

// parseTime parses a Midtrans timestamp, which is expressed in WIB without an
// explicit offset. It returns nil when the value is empty or unparseable so
// callers can treat the field as optional.
func parseTime(value string) *time.Time {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil
	}
	for _, layout := range []string{midtransTimeLayout, time.RFC3339Nano, time.RFC3339} {
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

// customExpiryFrom renders a lifetime as the minute-based custom_expiry object
// Midtrans expects. A non-positive duration yields nil (gateway default).
func customExpiryFrom(d time.Duration) *customExpiry {
	if d <= 0 {
		return nil
	}
	minutes := int(d / time.Minute)
	if d%time.Minute != 0 {
		minutes++
	}
	if minutes < 1 {
		minutes = 1
	}
	return &customExpiry{ExpiryDuration: minutes, Unit: "minute"}
}

// classifyStatus maps an unsuccessful Midtrans response onto a contract
// sentinel so the core engine can react with errors.Is.
func classifyStatus(httpStatus int, code string) error {
	switch {
	case httpStatus == 401 || httpStatus == 403:
		return provider.ErrUnauthorized
	case httpStatus == 404:
		return provider.ErrNotFound
	case httpStatus == 406:
		// Midtrans reports a reused order_id as "406 Not Acceptable".
		return provider.ErrDuplicateReference
	case httpStatus == 400 || httpStatus == 402 || httpStatus == 422:
		return provider.ErrInvalidRequest
	}
	switch {
	case strings.HasPrefix(code, "401"), strings.HasPrefix(code, "403"):
		return provider.ErrUnauthorized
	case strings.HasPrefix(code, "404"):
		return provider.ErrNotFound
	case strings.HasPrefix(code, "406"):
		return provider.ErrDuplicateReference
	case strings.HasPrefix(code, "4"):
		return provider.ErrInvalidRequest
	default:
		return provider.ErrGateway
	}
}

// customerDetailsFrom splits a unified customer name into Midtrans' first/last
// name fields, since Midtrans does not accept a single "name".
func customerDetailsFrom(c provider.Customer) *customerDetails {
	first, last := splitName(c.Name)
	if first == "" && c.Email == "" && c.Phone == "" {
		return nil
	}
	return &customerDetails{
		FirstName: first,
		LastName:  last,
		Email:     strings.TrimSpace(c.Email),
		Phone:     strings.TrimSpace(c.Phone),
	}
}

// splitName divides a display name into a first and last name at the last
// whitespace. Midtrans requires first_name when customer_details is present.
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

// itemDetailsFrom translates unified line items into Midtrans' item_details.
func itemDetailsFrom(items []provider.Item) []itemDetail {
	if len(items) == 0 {
		return nil
	}
	out := make([]itemDetail, 0, len(items))
	for _, it := range items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			continue
		}
		out = append(out, itemDetail{
			ID:       it.ID,
			Price:    it.Price,
			Quantity: it.Quantity,
			Name:     name,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// mapActions translates Midtrans' "actions" array into unified payment actions.
// The v2 QR code is dropped when a v1 code is already present, since both point
// at the same QR payload.
func mapActions(actions []action) []provider.PaymentAction {
	var out []provider.PaymentAction
	qrAdded := false
	for _, a := range actions {
		url := strings.TrimSpace(a.URL)
		if url == "" {
			continue
		}
		switch a.Name {
		case "generate-qr-code":
			out = append(out, provider.PaymentAction{
				Type: provider.ActionURL, Value: url, Description: "QR code image",
			})
			qrAdded = true
		case "generate-qr-code-v2":
			if qrAdded {
				continue
			}
			out = append(out, provider.PaymentAction{
				Type: provider.ActionURL, Value: url, Description: "QR code image",
			})
			qrAdded = true
		case "deeplink-redirect":
			out = append(out, provider.PaymentAction{
				Type: provider.ActionDeeplink, Value: url, Description: "Open the payment app",
			})
		case "redirect-url", "redirect":
			out = append(out, provider.PaymentAction{
				Type: provider.ActionURL, Value: url, Description: "Payment page",
			})
		}
	}
	return out
}

// qrURL returns the first QR image URL in an actions array.
func qrURL(actions []action) string {
	for _, a := range actions {
		if (a.Name == "generate-qr-code" || a.Name == "generate-qr-code-v2") && strings.TrimSpace(a.URL) != "" {
			return strings.TrimSpace(a.URL)
		}
	}
	return ""
}

// paymentCode extracts the human-facing payment identifier for a response: a VA
// number, a Mandiri bill key, an over-the-counter code or the QR image URL.
func paymentCode(out *transactionResponse) string {
	switch {
	case len(out.VANumbers) > 0:
		return strings.TrimSpace(out.VANumbers[0].VANumber)
	case strings.TrimSpace(out.PermataVANumber) != "":
		return strings.TrimSpace(out.PermataVANumber)
	case strings.TrimSpace(out.BillKey) != "":
		return strings.TrimSpace(out.BillKey)
	case strings.TrimSpace(out.PaymentCode) != "":
		return strings.TrimSpace(out.PaymentCode)
	default:
		return qrURL(out.Actions)
	}
}

// eventType builds a descriptive event name from the Midtrans payment type and
// transaction status, e.g. "qris.settlement" or "bank_transfer.expire".
func eventType(paymentType, status string) string {
	pt := strings.TrimSpace(paymentType)
	st := strings.TrimSpace(status)
	switch {
	case pt == "" && st == "":
		return ""
	case pt == "":
		return st
	case st == "":
		return pt
	default:
		return pt + "." + st
	}
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

// truncate bounds an error message taken from a raw gateway response.
func truncate(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
